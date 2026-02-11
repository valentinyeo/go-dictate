package keyboard

import (
	"context"
	"fmt"
	"time"
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

	// Add space before text if we've already typed something
	// (i.e., this isn't the very first text being typed)
	textToType := text
	if s.lastChar != 0 {
		textToType = " " + text
	}

	logger.Typed(textToType)

	// Type the text and track the last character
	for _, ch := range textToType {
		s.sendUnicodeChar(ch)
		s.lastChar = ch
		time.Sleep(2 * time.Millisecond)
	}
}

func (s *Simulator) sendUnicodeChar(ch rune) {
	// Send both key-down and key-up in a single atomic SendInput call.
	// Without the key-up event, consecutive identical characters (e.g. "rr", "ll")
	// get coalesced by the target application because it never sees a release
	// between the two presses.
	inputs := [2]input{
		{
			inputType: inputKeyboard,
			ki: keyboardInput{
				wScan:   uint16(ch),
				dwFlags: keyeventfUnicode,
			},
		},
		{
			inputType: inputKeyboard,
			ki: keyboardInput{
				wScan:   uint16(ch),
				dwFlags: keyeventfUnicode | keyeventfKeyUp,
			},
		},
	}

	ret, _, err := sendInputProc.Call(
		uintptr(2),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(unsafe.Sizeof(inputs[0])),
	)

	if ret == 0 {
		fmt.Printf("[Keyboard] SendInput failed: %v\n", err)
	}
}
