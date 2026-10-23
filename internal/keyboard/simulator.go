package keyboard

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/valen/go-dictate/internal/hotkey"
	"github.com/valen/go-dictate/internal/logger"
	"golang.org/x/sys/windows"
)

const (
	inputKeyboard    = 1
	keyeventfUnicode = 0x0004
	keyeventfKeyUp   = 0x0002
)

type keyboardInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
	_           [8]byte
}

type input struct {
	inputType uint32
	ki        keyboardInput
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	sendInputProc           = user32.NewProc("SendInput")
	getForegroundWindowProc = user32.NewProc("GetForegroundWindow")
	getAsyncKeyStateProc    = user32.NewProc("GetAsyncKeyState")
	getClassNameProc        = user32.NewProc("GetClassNameW")
	postMessageProc         = user32.NewProc("PostMessageW")
)

const (
	wmSysCommand   = 0x0112
	scConsolePaste = 0xFFF1
)

func foregroundWindowClass() (uintptr, string) {
	hwnd, _, _ := getForegroundWindowProc.Call()
	if hwnd == 0 {
		return 0, ""
	}
	var buf [64]uint16
	n, _, _ := getClassNameProc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return hwnd, ""
	}
	return hwnd, windows.UTF16ToString(buf[:n])
}

type Simulator struct {
	lastChar       rune
	skipPeriodOnce bool
	typeMode       bool
	inputSink      func([]input) (int, error)
	modifierState  func() []string
	modifierWait   func(context.Context) bool

	clipMu          sync.Mutex
	clipSaved       bool
	savedClip       string
	savedClipIsText bool
	lastPasted      clipboardSnapshot
	clipboard       pasteClipboard
	pasteWindow     func() (uintptr, string)
	restoreTimer    *time.Timer
	restoreAfter    func(time.Duration, func()) *time.Timer
	pasteGen        uint64
}

func NewSimulator() *Simulator {
	return &Simulator{
		lastChar: 0,
	}
}

func (s *Simulator) SetTypeMode(enabled bool) {
	s.typeMode = enabled
}

func (s *Simulator) ResetContext() {
	s.skipPeriodOnce = true
}

func (s *Simulator) Run(ctx context.Context, textRx <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case text, ok := <-textRx:
			if !ok {
				return
			}
			payload := s.preparePayload(text)
		drain:
			for {
				select {
				case more, ok := <-textRx:
					if !ok {
						break drain
					}
					payload += s.preparePayload(more)
				default:
					break drain
				}
			}
			waitCtx, cancel := context.WithTimeout(ctx, modifierWaitLimit)
			hotkey.WaitForModifiersReleased(waitCtx)
			cancel()
			if ctx.Err() != nil {
				return
			}
			s.deliver(ctx, payload)
		}
	}
}

func (s *Simulator) preparePayload(text string) string {
	if text == "" {
		return ""
	}

	raw := false
	if text[0] == '\x01' {
		raw = true
		text = text[1:]
		if text == "" {
			return ""
		}
	}

	textToType := text
	if !raw {
		if s.lastChar != 0 {
			firstRune, _ := utf8.DecodeRuneInString(text)
			if !s.skipPeriodOnce && needsPeriod(s.lastChar, firstRune) {
				textToType = ". " + text
			} else {
				textToType = " " + text
			}
		}
		s.skipPeriodOnce = false
	}

	logger.Typed(textToType)

	for _, ch := range textToType {
		if ch != '\b' {
			s.lastChar = ch
		}
	}
	return textToType
}

func (s *Simulator) deliver(ctx context.Context, payload string) {
	var observed []string
	for len(payload) > 0 {
		modifiers, ready := s.waitForInput(ctx)
		if len(modifiers) > 0 {
			observed = modifiers
		}
		if !ready {
			logger.Info("[Keyboard] Delivery path=type clipboard_match=not_checked modifiers=%s cancelled=true", strings.Join(observed, ","))
			return
		}
		if payload[0] == '\b' {
			n := 0
			for n < len(payload) && payload[n] == '\b' {
				n++
			}
			s.sendInputs(ctx, backspaceEvents(n))
			logger.Info("[Keyboard] Delivery path=type clipboard_match=not_checked modifiers=%s", strings.Join(observed, ","))
			payload = payload[n:]
			continue
		}
		seg := payload
		if i := strings.IndexByte(payload, '\b'); i >= 0 {
			seg = payload[:i]
			payload = payload[i:]
		} else {
			payload = ""
		}
		observed = s.insertText(ctx, seg, observed)
	}
}

func (s *Simulator) insertText(ctx context.Context, text string, modifiers []string) []string {
	if text == "" {
		return modifiers
	}
	path, matched := "type", "not_checked"
	defer func() {
		logger.Info("[Keyboard] Delivery path=%s clipboard_match=%s modifiers=%s", path, matched, strings.Join(modifiers, ","))
	}()
	if len(modifiers) > 0 {
		s.sendInputs(ctx, unicodeTypingEvents(text))
		return modifiers
	}
	if s.typeMode {
		s.sendInputs(ctx, typingEvents(text))
		return modifiers
	}
	path, matched, modifiers = s.pasteText(ctx, text, modifiers)
	return modifiers
}

const clipboardRestoreDelay = 2 * time.Second
const modifierWaitLimit = time.Second

func (s *Simulator) pasteText(ctx context.Context, text string, modifiers []string) (path, matched string, observed []string) {
	path, matched, observed = "type", "false", modifiers
	s.clipMu.Lock()
	defer s.clipMu.Unlock()
	s.pasteGen++
	gen := s.pasteGen
	if s.clipboard == nil {
		s.clipboard = windowsClipboard{}
	}
	if !s.clipSaved {
		prev, err := s.clipboard.read()
		s.savedClipIsText = err == nil
		s.savedClip = prev.text
		if err != nil {
			logger.Debug("[Keyboard] Previous clipboard unavailable or not plain text - will not restore")
		}
		s.clipSaved = true
	}
	window := s.pasteWindow
	if window == nil {
		window = foregroundWindowClass
	}
	hwnd, class := window()
	written, _ := guardedPaste(s.clipboard, text, func() {
		time.Sleep(15 * time.Millisecond)
	}, func() {
		matched = "true"
		held, ready := s.waitForInput(ctx)
		if len(held) > 0 {
			observed = held
		}
		if !ready {
			return
		}
		if len(held) > 0 {
			s.sendInputs(ctx, unicodeTypingEvents(text))
			return
		}
		path = "paste"
		if class == "ConsoleWindowClass" {
			postMessageProc.Call(hwnd, wmSysCommand, scConsolePaste, 0)
		} else {
			s.sendInputs(ctx, pasteKeyEvents())
		}
	}, func(err error) {
		logger.Error("[Keyboard] Paste skipped (%v) - falling back to Unicode typing", err)
		held, ready := s.waitForInput(ctx)
		if len(held) > 0 {
			observed = held
		}
		if ready {
			s.sendInputs(ctx, unicodeTypingEvents(text))
		}
	})
	if written.sequence != 0 {
		s.lastPasted = written
	}
	if s.restoreTimer != nil {
		s.restoreTimer.Stop()
	}
	after := s.restoreAfter
	if after == nil {
		after = time.AfterFunc
	}
	s.restoreTimer = after(clipboardRestoreDelay, func() {
		s.restoreClipboard(gen)
	})
	return
}

func (s *Simulator) restoreClipboard(gen uint64) {
	s.clipMu.Lock()
	defer s.clipMu.Unlock()
	if gen != s.pasteGen {
		logger.Info("[Keyboard] Clipboard restore result=skipped_stale")
		return
	}
	if !s.clipSaved {
		logger.Info("[Keyboard] Clipboard restore result=skipped_no_text")
		return
	}
	result := "skipped_no_text"
	if s.savedClipIsText {
		restored, err := s.clipboard.restore(s.lastPasted, s.savedClip)
		if err != nil {
			result = "error"
		} else if !restored {
			result = "skipped_not_ours"
		} else {
			result = "restored"
		}
	}
	logger.Info("[Keyboard] Clipboard restore result=%s", result)
	s.clipSaved = false
	s.lastPasted = clipboardSnapshot{}
}

func unicodeTypingEvents(text string) []input {
	units := utf16.Encode([]rune(text))
	inputs := make([]input, 0, len(units)*2)
	for _, unit := range units {
		inputs = append(inputs,
			input{inputType: inputKeyboard, ki: keyboardInput{wScan: unit, dwFlags: keyeventfUnicode}},
			input{inputType: inputKeyboard, ki: keyboardInput{wScan: unit, dwFlags: keyeventfUnicode | keyeventfKeyUp}},
		)
	}
	return inputs
}

func typingEvents(text string) []input {
	runes := []rune(text)
	inputs := make([]input, 0, len(runes)*2)
	for _, ch := range runes {
		if ch == ' ' {
			inputs = append(inputs,
				input{inputType: inputKeyboard, ki: keyboardInput{wVk: vkSpace}},
				input{inputType: inputKeyboard, ki: keyboardInput{wVk: vkSpace, dwFlags: keyeventfKeyUp}},
			)
			continue
		}
		inputs = append(inputs,
			input{inputType: inputKeyboard, ki: keyboardInput{wScan: uint16(ch), dwFlags: keyeventfUnicode}},
			input{inputType: inputKeyboard, ki: keyboardInput{wScan: uint16(ch), dwFlags: keyeventfUnicode | keyeventfKeyUp}},
		)
	}
	return inputs
}

func backspaceEvents(n int) []input {
	inputs := make([]input, 0, n*2)
	for i := 0; i < n; i++ {
		inputs = append(inputs,
			input{inputType: inputKeyboard, ki: keyboardInput{wVk: vkBack}},
			input{inputType: inputKeyboard, ki: keyboardInput{wVk: vkBack, dwFlags: keyeventfKeyUp}},
		)
	}
	return inputs
}

func pasteKeyEvents() []input {
	return []input{
		{inputType: inputKeyboard, ki: keyboardInput{wVk: vkControl}},
		{inputType: inputKeyboard, ki: keyboardInput{wVk: vkV}},
		{inputType: inputKeyboard, ki: keyboardInput{wVk: vkV, dwFlags: keyeventfKeyUp}},
		{inputType: inputKeyboard, ki: keyboardInput{wVk: vkControl, dwFlags: keyeventfKeyUp}},
	}
}

func (s *Simulator) modifiersDown() []string {
	if s.modifierState != nil {
		return s.modifierState()
	}
	var down []string
	for _, key := range []struct {
		vk   uintptr
		name string
	}{{0x10, "Shift"}, {0x11, "Ctrl"}, {0x12, "Alt"}, {0x5b, "LWin"}, {0x5c, "RWin"}} {
		value, _, _ := getAsyncKeyStateProc.Call(key.vk)
		if value&0x8000 != 0 {
			down = append(down, key.name)
		}
	}
	return down
}

func (s *Simulator) waitForInput(ctx context.Context) ([]string, bool) {
	modifiers := s.modifiersDown()
	observed := modifiers
	wait := s.modifierWait
	if wait == nil {
		wait = hotkey.WaitForModifiersReleased
	}
	for len(modifiers) > 0 && ctx.Err() == nil {
		logger.Info("[Keyboard] Delivery held modifiers=%s; waiting for release", strings.Join(modifiers, ","))
		waitCtx, cancel := context.WithTimeout(ctx, modifierWaitLimit)
		wait(waitCtx)
		cancel()
		modifiers = s.modifiersDown()
	}
	return observed, ctx.Err() == nil
}

func (s *Simulator) sendInputs(ctx context.Context, inputs []input) {
	if len(inputs) == 0 {
		return
	}
	if ctx.Err() != nil {
		return
	}
	sink := s.inputSink
	if sink == nil {
		sink = func(events []input) (int, error) {
			ret, _, err := sendInputProc.Call(
				uintptr(len(events)),
				uintptr(unsafe.Pointer(&events[0])),
				uintptr(unsafe.Sizeof(events[0])),
			)
			return int(ret), err
		}
	}
	const maxBlockedRetries = 10
	offset, blocked := 0, 0
	for offset < len(inputs) {
		n, err := sink(inputs[offset:])
		if n > 0 && n <= len(inputs)-offset {
			offset += n
			blocked = 0
			continue
		}
		blocked++
		if blocked < maxBlockedRetries {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		logger.Error("[Keyboard] SendInput incomplete (%d/%d events): %v", offset, len(inputs), err)
		var cleanup []input
		for _, event := range inputs[:offset] {
			if event.ki.dwFlags&keyeventfKeyUp == 0 {
				cleanup = append(cleanup, event)
				continue
			}
			for i := len(cleanup) - 1; i >= 0; i-- {
				if cleanup[i].ki.wVk == event.ki.wVk && cleanup[i].ki.wScan == event.ki.wScan {
					cleanup = append(cleanup[:i], cleanup[i+1:]...)
					break
				}
			}
		}
		for i := range cleanup {
			cleanup[i].ki.dwFlags |= keyeventfKeyUp
		}
		if len(cleanup) > 0 {
			if n, err := sink(cleanup); n != len(cleanup) {
				logger.Error("[Keyboard] SendInput key release incomplete (%d/%d events): %v", n, len(cleanup), err)
			}
		}
		return
	}
}

func needsPeriod(lastChar, firstChar rune) bool {
	if lastChar == '.' || lastChar == '!' || lastChar == '?' || lastChar == ':' || lastChar == ';' || lastChar == ',' {
		return false
	}
	return unicode.IsUpper(firstChar)
}

const (
	vkBack    = 0x08
	vkSpace   = 0x20
	vkControl = 0x11
	vkV       = 0x56
)
