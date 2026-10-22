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
	getClipboardSequenceProc    = user32.NewProc("GetClipboardSequenceNumber")
	closeClipboardProc          = user32.NewProc("CloseClipboard")
	emptyClipboardProc          = user32.NewProc("EmptyClipboard")
	getClipboardDataProc        = user32.NewProc("GetClipboardData")
	setClipboardDataProc        = user32.NewProc("SetClipboardData")
	registerClipboardFormatProc = user32.NewProc("RegisterClipboardFormatW")
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

type windowsClipboard struct{}

func (windowsClipboard) sequence() uint32 {
	value, _, _ := getClipboardSequenceProc.Call()
	return uint32(value)
}

func (c windowsClipboard) read() (clipboardSnapshot, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboardRetry(); err != nil {
		return clipboardSnapshot{}, err
	}
	text, err := c.readOpen()
	sequence := c.sequence()
	closeClipboardProc.Call()
	if err != nil {
		return clipboardSnapshot{}, err
	}
	closedSequence := c.sequence()
	if closedSequence != sequence {
		return clipboardSnapshot{}, fmt.Errorf("clipboard changed during read")
	}
	return clipboardSnapshot{text: text, sequence: closedSequence}, nil
}

func (c windowsClipboard) readOpen() (string, error) {
	h, _, _ := getClipboardDataProc.Call(cfUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("clipboard text unavailable")
	}
	ptr, _, _ := globalLockProc.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("clipboard text lock failed")
	}
	text := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(ptr)))
	globalUnlockProc.Call(h)
	return text, nil
}

func (c windowsClipboard) write(text string) (clipboardSnapshot, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboardRetry(); err != nil {
		return clipboardSnapshot{}, err
	}
	err := writeClipboardTextOpen(text)
	closeClipboardProc.Call()
	if err != nil {
		return clipboardSnapshot{}, err
	}
	sequence := c.sequence()
	written, err := c.read()
	if err != nil {
		return clipboardSnapshot{}, err
	}
	if written.sequence != sequence {
		return clipboardSnapshot{}, fmt.Errorf("clipboard changed after write")
	}
	return written, nil
}

func (c windowsClipboard) restore(expected clipboardSnapshot, text string) (bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboardRetry(); err != nil {
		return false, err
	}
	defer closeClipboardProc.Call()
	current, err := c.readOpen()
	if err != nil || !matchesClipboard(clipboardSnapshot{text: current, sequence: c.sequence()}, expected) {
		return false, nil
	}
	if err := writeClipboardTextOpen(text); err != nil {
		return false, err
	}
	return true, nil
}

func writeClipboardTextOpen(text string) error {
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

	if ret, _, _ := emptyClipboardProc.Call(); ret == 0 {
		return fmt.Errorf("EmptyClipboard failed")
	}
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
