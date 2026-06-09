package keyboard

import (
	"context"
	"fmt"
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
	user32       = windows.NewLazySystemDLL("user32.dll")
	sendInputProc = user32.NewProc("SendInput")
)

type Simulator struct{
	lastChar       rune
	skipPeriodOnce bool // suppress the auto-period for the next emit (set by ResetContext)
}

func NewSimulator() *Simulator {
	return &Simulator{
		lastChar: 0,
	}
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
			// SendInput batch. Ultra Fast mode emits many tiny fragments
			// (interim diffs + finals) in rapid bursts; injecting each as its
			// own SendInput call saturates the input queue of busy windows
			// (notably the Claude Code terminal), which then drops or repeats
			// keystrokes. Merging what is already waiting cuts the call count
			// without adding latency — the drain is non-blocking, so we only
			// absorb fragments that have already arrived.
			inputs := s.buildInputs(text)
		drain:
			for {
				select {
				case more, ok := <-textRx:
					if !ok {
						break drain
					}
					inputs = append(inputs, s.buildInputs(more)...)
				default:
					break drain
				}
			}
			s.sendInputs(inputs)
		}
	}
}

// buildInputs converts a single text fragment into SendInput key events,
// applying raw-mode (\x01) handling and auto-spacing, and advancing lastChar.
// It does not call SendInput so callers can coalesce several fragments into one
// atomic injection.
func (s *Simulator) buildInputs(text string) []input {
	if text == "" {
		return nil
	}

	// Raw mode: \x01 prefix means skip auto-spacing (used by interim result diffs)
	raw := false
	if text[0] == '\x01' {
		raw = true
		text = text[1:]
		if text == "" {
			return nil
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

	runes := []rune(textToType)
	inputs := make([]input, 0, len(runes)*2)
	for _, ch := range runes {
		if ch == '\b' {
			inputs = append(inputs,
				input{inputType: inputKeyboard, ki: keyboardInput{wVk: vkBack}},
				input{inputType: inputKeyboard, ki: keyboardInput{wVk: vkBack, dwFlags: keyeventfKeyUp}},
			)
		} else {
			inputs = append(inputs,
				input{inputType: inputKeyboard, ki: keyboardInput{wScan: uint16(ch), dwFlags: keyeventfUnicode}},
				input{inputType: inputKeyboard, ki: keyboardInput{wScan: uint16(ch), dwFlags: keyeventfUnicode | keyeventfKeyUp}},
			)
			s.lastChar = ch
		}
	}
	return inputs
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

const vkBack = 0x08 // VK_BACK virtual key code
