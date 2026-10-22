package keyboard

import (
	"context"
	"testing"
	"time"
)

func newIGSH562Simulator(t *testing.T) (*Simulator, *fakeIGSH524Clipboard, <-chan func()) {
	t.Helper()
	c := &fakeIGSH524Clipboard{current: clipboardSnapshot{text: "original clipboard", sequence: 1}}
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	restores := make(chan func(), 4)
	s := &Simulator{
		clipboard:     c,
		inputSink:     f.send,
		modifierState: f.modifiers,
		pasteWindow:   func() (uintptr, string) { return 0, "Edit" },
		restoreAfter: func(delay time.Duration, restore func()) *time.Timer {
			if delay != clipboardRestoreDelay {
				t.Errorf("restore delay=%v, want %v", delay, clipboardRestoreDelay)
			}
			timer := time.NewTimer(0)
			<-timer.C
			restores <- restore
			return timer
		},
	}
	return s, c, restores
}

func TestIGSH562StaleRestoreDoesNotOverwriteNewPaste(t *testing.T) {
	s, c, restores := newIGSH562Simulator(t)
	if path, matched, _ := s.pasteText(context.Background(), "chunk A", nil); path != "paste" || matched != "true" {
		t.Fatalf("first paste: path=%s matched=%s", path, matched)
	}
	restoreA := <-restores
	if s.restoreTimer.Stop() {
		t.Fatal("first restore timer must already have expired")
	}
	started, done := make(chan struct{}), make(chan struct{})
	c.onRead = func(c *fakeIGSH524Clipboard) {
		c.onRead = nil
		go func() {
			defer close(done)
			if s.clipMu.TryLock() {
				s.clipMu.Unlock()
				t.Error("new paste must hold the clipboard mutex when the old restore fires")
			}
			close(started)
			restoreA()
		}()
		<-started
	}
	if path, matched, _ := s.pasteText(context.Background(), "chunk B", nil); path != "paste" || matched != "true" {
		t.Errorf("second paste: path=%s matched=%s", path, matched)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stale restore did not finish after the new paste unlocked")
	}
	if c.current.text != "chunk B" || c.writes != 2 {
		t.Fatalf("stale restore overwrote the new paste: clipboard=%+v writes=%d", c.current, c.writes)
	}
	if !s.clipSaved || s.savedClip != "original clipboard" || s.lastPasted != c.current {
		t.Fatal("stale restore discarded the current burst's saved clipboard")
	}
}

func TestIGSH562RestoreAfterBurstStillRestores(t *testing.T) {
	s, c, restores := newIGSH562Simulator(t)
	for _, text := range []string{"chunk A", "chunk B", "chunk C"} {
		if path, matched, _ := s.pasteText(context.Background(), text, nil); path != "paste" || matched != "true" {
			t.Fatalf("paste %q: path=%s matched=%s", text, path, matched)
		}
	}
	for i := 0; i < 2; i++ {
		restore := <-restores
		restore()
		if c.current.text != "chunk C" || c.writes != 3 || !s.clipSaved {
			t.Fatalf("earlier restore changed the final paste: clipboard=%+v writes=%d saved=%t", c.current, c.writes, s.clipSaved)
		}
	}
	restore := <-restores
	restore()
	if c.current.text != "original clipboard" || c.writes != 4 || s.clipSaved || s.lastPasted != (clipboardSnapshot{}) {
		t.Fatalf("final restore failed: clipboard=%+v writes=%d saved=%t lastPasted=%+v", c.current, c.writes, s.clipSaved, s.lastPasted)
	}
}
