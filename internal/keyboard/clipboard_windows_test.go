//go:build windows

package keyboard

import (
	"runtime"
	"testing"
)

func TestIGSH524WindowsRestoreRequiresSequenceAndText(t *testing.T) {
	clipboard := windowsClipboard{}
	previous, previousErr := clipboard.read()
	t.Cleanup(func() {
		if previousErr == nil {
			if _, err := clipboard.write(previous.text); err != nil {
				t.Errorf("restore test clipboard: %v", err)
			}
		}
	})
	for _, replacement := range []string{"dictation", "https://example.com/old"} {
		written, err := clipboard.write("dictation")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := clipboard.write(replacement); err != nil {
			t.Fatal(err)
		}
		restored, err := clipboard.restore(written, "saved")
		if err != nil || restored {
			t.Fatalf("restore must skip changed clipboard: restored=%v err=%v", restored, err)
		}
		current, err := clipboard.read()
		if err != nil || current.text != replacement {
			t.Fatalf("restore changed someone else's clipboard: %+v err=%v", current, err)
		}
	}
	written, err := clipboard.write("dictation")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := clipboard.restore(written, "saved")
	if err != nil || !restored {
		t.Fatalf("owned clipboard did not restore: restored=%v err=%v", restored, err)
	}
	current, err := clipboard.read()
	if err != nil || current.text != "saved" {
		t.Fatalf("restored clipboard=%+v err=%v", current, err)
	}
}

func TestIGSH524WindowsWriteHasOwnerAndSurvivesClose(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	clipboard := windowsClipboard{}
	previous, previousErr := clipboard.read()
	t.Cleanup(func() {
		if previousErr == nil {
			if _, err := clipboard.write(previous.text); err != nil {
				t.Errorf("restore test clipboard: %v", err)
			}
		}
	})
	closeWrite, err := openClipboardForWrite()
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer closeWrite()
		if err := writeClipboardTextOpen("invented clipboard text"); err != nil {
			t.Fatal(err)
		}
		owner, _, _ := user32.NewProc("GetClipboardOwner").Call()
		if owner == 0 {
			t.Fatal("clipboard write has no owner window")
		}
		thread, _, _ := user32.NewProc("GetWindowThreadProcessId").Call(owner, 0)
		current, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
		if thread != current {
			t.Fatal("clipboard owner belongs to another thread")
		}
	}()
	written, err := clipboard.read()
	if err != nil || written.text != "invented clipboard text" || written.sequence == 0 {
		t.Fatalf("clipboard text did not survive closing its writer: %+v err=%v", written, err)
	}
}
