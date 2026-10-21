//go:build windows

package keyboard

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type igsh524Message struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	x, y    int32
	private uint32
}

type igsh524HookEvent struct {
	vk, scan, flags, time uint32
	extra                 uintptr
}

type igsh524Edit struct {
	hwnd    uintptr
	thread  uint32
	changes chan struct{}
	done    chan struct{}
	pastes  atomic.Int32
	packets atomic.Int32
}

func newIGSH524Edit(t *testing.T) *igsh524Edit {
	t.Helper()
	e := &igsh524Edit{changes: make(chan struct{}, 1), done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(e.done)
		id, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
		e.thread = uint32(id)
		class, _ := windows.UTF16PtrFromString("EDIT")
		hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000|0x0004|0x0040, 0, 0, 200, 100, 0, 0, 0, 0)
		if hwnd == 0 {
			ready <- fmt.Errorf("create hidden EDIT: %v", err)
			return
		}
		e.hwnd = hwnd
		defer user32.NewProc("DestroyWindow").Call(hwnd)
		sendMessage := user32.NewProc("SendMessageW")
		callNext := user32.NewProc("CallNextHookEx")
		control := false
		callback := syscall.NewCallback(func(code, message, data uintptr) uintptr {
			if int32(code) >= 0 {
				event := (*igsh524HookEvent)(unsafe.Pointer(data))
				if event.flags&0x10 != 0 {
					down := message == 0x0100 || message == 0x0104
					switch event.vk {
					case vkControl:
						control = down
					case vkV:
						if down && control {
							sendMessage.Call(hwnd, 0x0302, 0, 0)
							e.pastes.Add(1)
						}
					case 0xe7:
						if down {
							sendMessage.Call(hwnd, 0x0102, uintptr(event.scan), 0)
							e.packets.Add(1)
						}
					}
					select {
					case e.changes <- struct{}{}:
					default:
					}
					return 1
				}
			}
			result, _, _ := callNext.Call(0, code, message, data)
			return result
		})
		module, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
		hook, _, err := user32.NewProc("SetWindowsHookExW").Call(13, callback, module, 0)
		if hook == 0 {
			ready <- fmt.Errorf("install injected-input hook: %v", err)
			return
		}
		defer user32.NewProc("UnhookWindowsHookEx").Call(hook)
		ready <- nil
		var message igsh524Message
		for {
			result, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hidden EDIT message loop did not start")
	}
	t.Cleanup(func() {
		user32.NewProc("PostThreadMessageW").Call(uintptr(e.thread), 0x0012, 0, 0)
		select {
		case <-e.done:
		case <-time.After(5 * time.Second):
			t.Error("hidden EDIT message loop did not stop")
		}
	})
	return e
}

func (e *igsh524Edit) waitText(t *testing.T, want string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var got string
	for {
		buffer := make([]uint16, len(want)*2+256)
		var result uintptr
		ok, _, err := user32.NewProc("SendMessageTimeoutW").Call(e.hwnd, 0x000d, uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])), 0x0002, 1000, uintptr(unsafe.Pointer(&result)))
		if ok == 0 {
			t.Fatalf("read EDIT text: %v", err)
		}
		got = windows.UTF16ToString(buffer)
		if got == want {
			return
		}
		if strings.Contains(got, "https://example.com/old") || len(got) > len(want) {
			t.Fatalf("unexpected EDIT content: %q, want %q", got, want)
		}
		select {
		case <-e.changes:
		case <-deadline.C:
			t.Fatalf("EDIT input timeout: got %q, want %q", got, want)
		}
	}
}

type igsh524HistoryClipboard struct {
	windowsClipboard
	changed chan chan error
}

func (c igsh524HistoryClipboard) write(text string) (clipboardSnapshot, error) {
	written, err := c.windowsClipboard.write(text)
	if err != nil {
		return written, err
	}
	ack := make(chan error, 1)
	select {
	case c.changed <- ack:
	case <-time.After(2 * time.Second):
		return written, fmt.Errorf("clipboard history writer did not receive change")
	}
	select {
	case err := <-ack:
		return written, err
	case <-time.After(2 * time.Second):
		return written, fmt.Errorf("clipboard history writer did not acknowledge change")
	}
}

func TestIGSH524HiddenEditClipboardHistory50Pastes(t *testing.T) {
	e := newIGSH524Edit(t)
	clipboard := windowsClipboard{}
	previous, previousErr := clipboard.read()
	if _, err := clipboard.write("https://example.com/old"); err != nil {
		t.Fatal(err)
	}
	s := NewSimulator()
	s.clipboard = clipboard
	s.pasteWindow = func() (uintptr, string) { return e.hwnd, "Edit" }
	t.Cleanup(func() {
		s.clipMu.Lock()
		if s.restoreTimer != nil {
			s.restoreTimer.Stop()
		}
		s.clipSaved = false
		s.clipMu.Unlock()
		if previousErr == nil {
			if _, err := clipboard.write(previous.text); err != nil {
				t.Errorf("restore test clipboard: %v", err)
			}
		}
	})
	s.insertText("control ")
	e.waitText(t, "control ")
	if e.pastes.Load() != 1 || e.packets.Load() != 0 {
		t.Fatal("positive control did not use real Ctrl+V SendInput")
	}
	user32.NewProc("SendMessageW").Call(e.hwnd, 0x000c, 0, 0)
	changed := make(chan chan error)
	stop, done, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var replacements atomic.Int32
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		lastSequence := clipboard.sequence()
		close(started)
		replace := func() error {
			sequence := clipboard.sequence()
			if sequence == lastSequence {
				return nil
			}
			current, err := clipboard.read()
			if err != nil {
				return err
			}
			if current.text != "https://example.com/old" {
				if _, err := clipboard.write("https://example.com/old"); err != nil {
					return err
				}
				replacements.Add(1)
			}
			lastSequence = clipboard.sequence()
			return nil
		}
		for {
			select {
			case ack := <-changed:
				ack <- replace()
			case <-ticker.C:
				_ = replace()
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("clipboard history writer did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("clipboard history writer did not start")
	}
	s.clipboard = igsh524HistoryClipboard{changed: changed}
	want := ""
	for i := 0; i < 50; i++ {
		text := fmt.Sprintf("dictated %02d café 😀; ", i)
		want += text
		s.insertText(text)
		e.waitText(t, want)
	}
	if replacements.Load() != 50 || e.pastes.Load() != 1 || e.packets.Load() == 0 {
		t.Fatalf("replacements=%d Ctrl+V=%d Unicode=%d", replacements.Load(), e.pastes.Load(), e.packets.Load())
	}
	t.Log("50 ordered dictated insertions; 50 real clipboard replacements; no old URL; Unicode SendInput fallback")
}
