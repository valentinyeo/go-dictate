package keyboard

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	openClipboardProc          = user32.NewProc("OpenClipboard")
	closeClipboardProc         = user32.NewProc("CloseClipboard")
	emptyClipboardProc         = user32.NewProc("EmptyClipboard")
	getClipboardDataProc       = user32.NewProc("GetClipboardData")
	setClipboardDataProc       = user32.NewProc("SetClipboardData")
	isClipboardFormatAvailProc = user32.NewProc("IsClipboardFormatAvailable")
	globalAllocProc            = kernel32.NewProc("GlobalAlloc")
	globalFreeProc             = kernel32.NewProc("GlobalFree")
	globalLockProc             = kernel32.NewProc("GlobalLock")
	globalUnlockProc           = kernel32.NewProc("GlobalUnlock")
)

// openClipboardRetry opens the clipboard, retrying briefly — another process
// (clipboard managers, the target app reading our paste) may hold it.
func openClipboardRetry() error {
	for i := 0; i < 10; i++ {
		ret, _, _ := openClipboardProc.Call(0)
		if ret != 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("clipboard busy")
}

// readClipboardText returns the clipboard's CF_UNICODETEXT content.
// ok is false when the clipboard is empty, holds a non-text format
// (image, files), or cannot be opened.
func readClipboardText() (text string, ok bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if avail, _, _ := isClipboardFormatAvailProc.Call(cfUnicodeText); avail == 0 {
		return "", false
	}
	if err := openClipboardRetry(); err != nil {
		return "", false
	}
	defer closeClipboardProc.Call()

	h, _, _ := getClipboardDataProc.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	ptr, _, _ := globalLockProc.Call(h)
	if ptr == 0 {
		return "", false
	}
	text = windows.UTF16PtrToString((*uint16)(unsafe.Pointer(ptr)))
	globalUnlockProc.Call(h)
	return text, true
}

// writeClipboardText replaces the clipboard content with the given text.
func writeClipboardText(text string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	u16, err := windows.UTF16FromString(text)
	if err != nil {
		return fmt.Errorf("utf16 conversion: %w", err)
	}

	size := uintptr(len(u16) * 2)
	h, _, _ := globalAllocProc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := globalLockProc.Call(h)
	if ptr == 0 {
		globalFreeProc.Call(h)
		return fmt.Errorf("GlobalLock failed")
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16))
	copy(dst, u16)
	globalUnlockProc.Call(h)

	if err := openClipboardRetry(); err != nil {
		globalFreeProc.Call(h)
		return err
	}
	defer closeClipboardProc.Call()

	emptyClipboardProc.Call()
	if ret, _, _ := setClipboardDataProc.Call(cfUnicodeText, h); ret == 0 {
		globalFreeProc.Call(h)
		return fmt.Errorf("SetClipboardData failed")
	}
	// On success the system owns the memory — do not free.
	return nil
}
