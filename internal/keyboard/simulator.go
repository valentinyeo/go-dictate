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
	inputKeyboard = 1
	keyeventfUnicode = 0x0004
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
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Simulator) sendUnicodeChar(ch rune) {
	inp := input{
		inputType: inputKeyboard,
		ki: keyboardInput{
			wVk:     0,
			wScan:   uint16(ch),
			dwFlags: keyeventfUnicode,
			time:    0,
			dwExtraInfo: 0,
		},
	}

	ret, _, err := sendInputProc.Call(
		uintptr(1),
		uintptr(unsafe.Pointer(&inp)),
		uintptr(unsafe.Sizeof(inp)),
	)

	if ret == 0 {
		fmt.Printf("[Keyboard] SendInput failed: %v\n", err)
	}
}
