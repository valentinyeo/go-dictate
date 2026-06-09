package keyboard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

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
	user32        = windows.NewLazySystemDLL("user32.dll")
	sendInputProc = user32.NewProc("SendInput")
)

type Simulator struct {
	lastChar       rune
	skipPeriodOnce bool // suppress the auto-period for the next emit (set by ResetContext)
	typeMode       bool // config escape hatch: per-character key events instead of paste

	// Clipboard save/restore state for paste injection. The user's clipboard
	// is captured before the first paste of a burst and restored after a
	// debounce window with no further pastes. Guarded by clipMu because the
	// restore fires on a timer goroutine.
	clipMu          sync.Mutex
	clipSaved       bool
	savedClip       string
	savedClipIsText bool
	lastPasted      string
	restoreTimer    *time.Timer
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
			s.deliver(payload)
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
func (s *Simulator) deliver(payload string) {
	for len(payload) > 0 {
		if payload[0] == '\b' {
			n := 0
			for n < len(payload) && payload[n] == '\b' {
				n++
			}
			s.sendInputs(backspaceEvents(n))
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
		s.insertText(seg)
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
func (s *Simulator) insertText(text string) {
	if text == "" {
		return
	}
	if s.typeMode {
		s.sendInputs(typingEvents(text))
		return
	}
	if err := s.pasteText(text); err != nil {
		logger.Error("[Keyboard] Paste failed (%v) - falling back to typing", err)
		s.sendInputs(typingEvents(text))
	}
}

const clipboardRestoreDelay = 1500 * time.Millisecond

// pasteText puts text on the clipboard and sends Ctrl+V. The user's previous
// clipboard text is captured before the first paste of a burst and restored
// after clipboardRestoreDelay with no further pastes (debounced), and only if
// the clipboard still holds our pasted text — never clobbering something the
// user copied in the meantime. Non-text clipboard content (images, files)
// cannot be saved/restored and is lost; that limitation is logged.
func (s *Simulator) pasteText(text string) error {
	s.clipMu.Lock()
	defer s.clipMu.Unlock()

	if !s.clipSaved {
		if prev, ok := readClipboardText(); ok {
			s.savedClip = prev
			s.savedClipIsText = true
		} else {
			s.savedClipIsText = false
			logger.Debug("[Keyboard] Previous clipboard not plain text - will not restore")
		}
		s.clipSaved = true
	}

	if err := writeClipboardText(text); err != nil {
		// Best effort: put the user's text back before falling back to typing.
		if s.savedClipIsText {
			_ = writeClipboardText(s.savedClip)
		}
		s.clipSaved = false
		return err
	}
	s.lastPasted = text

	// Small settle so slow clipboard listeners see the new content before
	// the paste keystroke arrives.
	time.Sleep(15 * time.Millisecond)
	s.sendInputs(pasteKeyEvents())

	if s.restoreTimer != nil {
		s.restoreTimer.Stop()
	}
	s.restoreTimer = time.AfterFunc(clipboardRestoreDelay, s.restoreClipboard)
	return nil
}

// restoreClipboard runs on the debounce timer after a paste burst ends.
func (s *Simulator) restoreClipboard() {
	s.clipMu.Lock()
	defer s.clipMu.Unlock()
	if !s.clipSaved {
		return
	}
	if cur, ok := readClipboardText(); ok && cur == s.lastPasted && s.savedClipIsText {
		if err := writeClipboardText(s.savedClip); err != nil {
			logger.Error("[Keyboard] Failed to restore clipboard: %v", err)
		}
	}
	s.clipSaved = false
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

// sendInputs injects every event via SendInput. SendInput returns the number of
// events it actually inserted and stops at the first one blocked by another
// thread injecting input — the remainder are silently lost unless we resume
// from the offset. We loop until all events are injected, backing off briefly
// only when fully blocked (ret == 0). The normal path sends everything in one
// call, so there is no added latency when injection is not contended.
func (s *Simulator) sendInputs(inputs []input) {
	if len(inputs) == 0 {
		return
	}
	const maxBlockedRetries = 10
	blocked := 0
	for off := 0; off < len(inputs); {
		remaining := inputs[off:]
		ret, _, err := sendInputProc.Call(
			uintptr(len(remaining)),
			uintptr(unsafe.Pointer(&remaining[0])),
			uintptr(unsafe.Sizeof(remaining[0])),
		)
		n := int(ret)
		if n <= 0 {
			blocked++
			if blocked >= maxBlockedRetries {
				fmt.Printf("[Keyboard] SendInput blocked, lost %d of %d events: %v\n", len(remaining), len(inputs), err)
				return
			}
			time.Sleep(2 * time.Millisecond)
			continue
		}
		off += n
		blocked = 0
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
