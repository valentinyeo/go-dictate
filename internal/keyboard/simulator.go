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
	_           [8]byte // padding
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
	scConsolePaste = 0xFFF1 // conhost system-menu "Edit > Paste" command ID
)

// foregroundWindowClass returns the focused top-level window and its class name.
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
	skipPeriodOnce bool // suppress the auto-period for the next emit (set by ResetContext)
	typeMode       bool // config escape hatch: per-character key events instead of paste
	inputSink      func([]input) (int, error)
	modifierState  func() []string
	modifierWait   func(context.Context) bool

	// Clipboard save/restore state for paste injection. The user's clipboard
	// is captured before the first paste of a burst and restored after a
	// debounce window with no further pastes. Guarded by clipMu because the
	// restore fires on a timer goroutine.
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

// SetTypeMode switches text delivery to legacy per-character key events
// (config: [keyboard] injection = "type"). Default is clipboard paste.
func (s *Simulator) SetTypeMode(enabled bool) {
	s.typeMode = enabled
}

// ResetContext is called when silence ends an utterance. A pause is NOT a new
// text field, so we must NOT clear lastChar — doing that dropped the leading
// space and glued the next word onto the previous text ("opened" + "Let's" ->
// "openedLet's") whenever the user paused >3s mid-dictation. Instead we keep
// lastChar (so the next word stays space-separated) and only suppress the
// auto-period for the next emit, so resuming in a genuinely fresh field can't
// inject a stray leading ". ".
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
			// Coalesce any fragments that are already queued into a single
			// delivery. Ultra Fast mode emits many tiny fragments (interim
			// diffs + finals) in rapid bursts; delivering each separately
			// saturates the target window's input handling. The drain is
			// non-blocking, so we only absorb fragments that have already
			// arrived — no added latency.
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

// preparePayload applies raw-mode (\x01) handling and auto-spacing to a single
// fragment, logs it, and advances lastChar. Delivery happens separately so
// several fragments can be coalesced into one paste/injection.
func (s *Simulator) preparePayload(text string) string {
	if text == "" {
		return ""
	}

	// Raw mode: \x01 prefix means skip auto-spacing (used by interim result diffs)
	raw := false
	if text[0] == '\x01' {
		raw = true
		text = text[1:]
		if text == "" {
			return ""
		}
	}

	// Add space before text if we've already typed something
	// (i.e., this isn't the very first text being typed)
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
		// Consumed by the first real (non-raw) emit after a ResetContext.
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

// deliver sends the payload to the focused window. Backspace runs become
// VK_BACK key events; text segments are pasted (or typed in typeMode).
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

// insertText delivers a text segment. Default is clipboard paste: terminals
// (ConPTY — cmd.exe, Windows Terminal, and therefore tmux/SSH inside them)
// silently DROP space key-events from injected keystroke batches, even within
// a single atomic SendInput call and regardless of Unicode vs VK_SPACE
// encoding ("cloud code" -> "cloudcode", "accuracy of whatever" ->
// "accuracyofwhatever"). Paste is immune: the terminal emulator itself reads
// the clipboard and forwards literal bytes to whatever runs inside, so tmux
// needs no clipboard support of its own. Falls back to key events if the
// clipboard is unavailable.
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

// typingEvents converts text to per-character SendInput key events
// (legacy path / paste fallback).
func typingEvents(text string) []input {
	runes := []rune(text)
	inputs := make([]input, 0, len(runes)*2)
	for _, ch := range runes {
		if ch == ' ' {
			// Real spacebar key — Unicode-injected spaces are dropped by
			// terminals far more often than VK_SPACE presses.
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

// needsPeriod returns true if a period should be inserted between two utterances.
// Detects when the previous utterance ended without sentence-ending punctuation
// and the new utterance starts with an uppercase letter (indicating a new sentence).
func needsPeriod(lastChar, firstChar rune) bool {
	if lastChar == '.' || lastChar == '!' || lastChar == '?' || lastChar == ':' || lastChar == ';' || lastChar == ',' {
		return false
	}
	return unicode.IsUpper(firstChar)
}

const (
	vkBack    = 0x08 // VK_BACK virtual key code
	vkSpace   = 0x20 // VK_SPACE virtual key code
	vkControl = 0x11 // VK_CONTROL virtual key code
	vkV       = 0x56 // 'V' virtual key code
)
