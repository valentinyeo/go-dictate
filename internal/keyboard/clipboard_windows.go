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
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	openClipboardProc           = user32.NewProc("OpenClipboard")
	closeClipboardProc          = user32.NewProc("CloseClipboard")
	emptyClipboardProc          = user32.NewProc("EmptyClipboard")
	getClipboardDataProc        = user32.NewProc("GetClipboardData")
	setClipboardDataProc        = user32.NewProc("SetClipboardData")
	registerClipboardFormatProc = user32.NewProc("RegisterClipboardFormatW")
	isClipboardFormatAvailProc  = user32.NewProc("IsClipboardFormatAvailable")
	globalAllocProc             = kernel32.NewProc("GlobalAlloc")
	globalFreeProc              = kernel32.NewProc("GlobalFree")
	globalLockProc              = kernel32.NewProc("GlobalLock")
	globalUnlockProc            = kernel32.NewProc("GlobalUnlock")
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

// writeClipboardText replaces the clipboard text, excluding both injection and
// restore writes from clipboard monitors, Windows history, and cloud sync.
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
	defer func() {
		if h != 0 {
			globalFreeProc.Call(h)
		}
	}()
	ptr, _, _ := globalLockProc.Call(h)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock failed")
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16))
	copy(dst, u16)
	globalUnlockProc.Call(h)

	if err := openClipboardRetry(); err != nil {
		return err
	}
	defer closeClipboardProc.Call()

	if ret, _, _ := emptyClipboardProc.Call(); ret == 0 {
		return fmt.Errorf("EmptyClipboard failed")
	}
	// Publish privacy metadata before text so a failed metadata write never
	// exposes untagged dictation to clipboard listeners on CloseClipboard.
	for _, format := range clipboardPrivacyFormats() {
		name, _ := windows.UTF16PtrFromString(format.name)
		id, _, _ := registerClipboardFormatProc.Call(uintptr(unsafe.Pointer(name)))
		if id == 0 {
			return fmt.Errorf("RegisterClipboardFormat failed: %s", format.name)
		}
		mem, _, _ := globalAllocProc.Call(gmemMoveable, unsafe.Sizeof(format.value))
		if mem == 0 {
			return fmt.Errorf("GlobalAlloc failed: %s", format.name)
		}
		raw, _, _ := globalLockProc.Call(mem)
		if raw == 0 {
			globalFreeProc.Call(mem)
			return fmt.Errorf("GlobalLock failed: %s", format.name)
		}
		*(*uint32)(unsafe.Pointer(raw)) = format.value
		globalUnlockProc.Call(mem)
		if ret, _, _ := setClipboardDataProc.Call(id, mem); ret == 0 {
			globalFreeProc.Call(mem)
			return fmt.Errorf("SetClipboardData failed: %s", format.name)
		}
	}
	if ret, _, _ := setClipboardDataProc.Call(cfUnicodeText, h); ret == 0 {
		return fmt.Errorf("SetClipboardData failed")
	}
	// On success the system owns the memory — do not free.
	h = 0
	return nil
}
