package keyboard

import (
	"context"
	"fmt"
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
	lastChar rune
}

func NewSimulator() *Simulator {
	return &Simulator{
		lastChar: 0,
	}
}

// ResetContext clears the lastChar state so the next utterance
// does not get a period prepended. Call when silence is detected
// (STT session ends) so each speech session starts fresh.
func (s *Simulator) ResetContext() {
	s.lastChar = 0
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
			s.typeText(text)
		}
	}
}

func (s *Simulator) typeText(text string) {
	if text == "" {
		return
	}

	// Raw mode: \x01 prefix means skip auto-spacing (used by interim result diffs)
	raw := false
	if text[0] == '\x01' {
		raw = true
		text = text[1:]
		if text == "" {
			return
		}
	}

	// Add space before text if we've already typed something
	// (i.e., this isn't the very first text being typed)
	textToType := text
	if !raw && s.lastChar != 0 {
		firstRune, _ := utf8.DecodeRuneInString(text)
		if needsPeriod(s.lastChar, firstRune) {
			textToType = ". " + text
		} else {
			textToType = " " + text
		}
	}

	logger.Typed(textToType)

	// Build a single batch of all key events and send atomically via SendInput.
	// This prevents the target application from dropping keystrokes (especially
	// spaces) that occurred when characters were sent individually with sleeps.
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

	if len(inputs) > 0 {
		ret, _, err := sendInputProc.Call(
			uintptr(len(inputs)),
			uintptr(unsafe.Pointer(&inputs[0])),
			uintptr(unsafe.Sizeof(inputs[0])),
		)
		if ret == 0 {
			fmt.Printf("[Keyboard] SendInput batch (%d events) failed: %v\n", len(inputs), err)
		}
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
