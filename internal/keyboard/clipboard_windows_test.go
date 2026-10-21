//go:build windows

package keyboard

import "testing"

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
