package keyboard

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

type fakeIGSH524Clipboard struct {
	current    clipboardSnapshot
	reads      int
	busyReads  int
	onRead     func(*fakeIGSH524Clipboard)
	onSequence func(*fakeIGSH524Clipboard)
	writes     int
	writeErr   error
}

func (c *fakeIGSH524Clipboard) write(text string) (clipboardSnapshot, error) {
	c.writes++
	if c.writeErr != nil {
		return clipboardSnapshot{}, c.writeErr
	}
	c.current = clipboardSnapshot{text: text, sequence: c.current.sequence + 1}
	return c.current, nil
}

func (c *fakeIGSH524Clipboard) read() (clipboardSnapshot, error) {
	c.reads++
	if c.onRead != nil {
		c.onRead(c)
	}
	if c.reads <= c.busyReads {
		return clipboardSnapshot{}, fmt.Errorf("busy")
	}
	return c.current, nil
}

func (c *fakeIGSH524Clipboard) sequence() uint32 {
	if c.onSequence != nil {
		c.onSequence(c)
	}
	return c.current.sequence
}

func (c *fakeIGSH524Clipboard) restore(expected clipboardSnapshot, text string) (bool, error) {
	if !matchesClipboard(c.current, expected) {
		return false, nil
	}
	_, err := c.write(text)
	return err == nil, err
}

func TestIGSH524MatchingClipboardPastes(t *testing.T) {
	c := &fakeIGSH524Clipboard{}
	pastes, fallbacks, waits := 0, 0, 0
	written, err := guardedPaste(c, "dictated text", func() { waits++ }, func() { pastes++ }, func(error) { fallbacks++ })
	if err != nil || pastes != 1 || fallbacks != 0 || c.reads != 2 || waits != 1 || written != c.current {
		t.Fatalf("written=%+v err=%v pastes=%d fallbacks=%d reads=%d waits=%d", written, err, pastes, fallbacks, c.reads, waits)
	}
}

func TestIGSH524RacingWriterUnicodeFallback(t *testing.T) {
	c := &fakeIGSH524Clipboard{onRead: func(c *fakeIGSH524Clipboard) {
		c.current = clipboardSnapshot{text: "https://example.com/old", sequence: c.current.sequence + 1}
	}}
	pastes := 0
	var typed []input
	_, err := guardedPaste(c, "dictated 😀 text", func() {}, func() { pastes++ }, func(error) {
		typed = unicodeTypingEvents("dictated 😀 text")
	})
	if err == nil || pastes != 0 || !reflect.DeepEqual(typed, unicodeTypingEvents("dictated 😀 text")) || len(typed) == 0 {
		t.Fatalf("err=%v pastes=%d typed=%v", err, pastes, typed)
	}
	for _, event := range typed {
		if event.ki.wVk != 0 || event.ki.dwFlags&keyeventfUnicode == 0 {
			t.Fatalf("fallback is not Unicode SendInput: %+v", event)
		}
	}
}

func TestIGSH524SequenceChangedBeforeSendFallback(t *testing.T) {
	for _, phase := range []string{"final-read", "dispatch"} {
		t.Run(phase, func(t *testing.T) {
			c := &fakeIGSH524Clipboard{}
			if phase == "final-read" {
				c.onRead = func(c *fakeIGSH524Clipboard) {
					if c.reads == 2 {
						c.current.sequence++
					}
				}
			} else {
				c.onSequence = func(c *fakeIGSH524Clipboard) { c.current.sequence++ }
			}
			pastes, fallbacks := 0, 0
			_, err := guardedPaste(c, "same text", func() {}, func() { pastes++ }, func(error) { fallbacks++ })
			if err == nil || pastes != 0 || fallbacks != 1 {
				t.Fatalf("err=%v pastes=%d fallbacks=%d", err, pastes, fallbacks)
			}
		})
	}
}

func TestIGSH524BusyClipboardBoundedRetry(t *testing.T) {
	for _, busyReads := range []int{2, 10} {
		c := &fakeIGSH524Clipboard{busyReads: busyReads}
		pastes, fallbacks, waits := 0, 0, 0
		_, err := guardedPaste(c, "text", func() { waits++ }, func() { pastes++ }, func(error) { fallbacks++ })
		if waits != 3 || (busyReads == 2 && (err != nil || pastes != 1 || fallbacks != 0)) || (busyReads == 10 && (err == nil || pastes != 0 || fallbacks != 1)) {
			t.Fatalf("busy=%d waits=%d err=%v pastes=%d fallbacks=%d", busyReads, waits, err, pastes, fallbacks)
		}
	}
}

func TestIGSH524RestoreSkippedAfterOwnershipLost(t *testing.T) {
	for _, text := range []string{"new user text", "dictation"} {
		c := &fakeIGSH524Clipboard{current: clipboardSnapshot{text: text, sequence: 8}}
		s := &Simulator{clipboard: c, clipSaved: true, savedClipIsText: true, savedClip: "old", lastPasted: clipboardSnapshot{text: "dictation", sequence: 7}}
		s.restoreClipboard()
		if c.writes != 0 || s.clipSaved || c.current.text != text {
			t.Fatalf("restore clobbered clipboard: %+v", c)
		}
	}
}

func TestIGSH524RestoreAfterConsumptionDelay(t *testing.T) {
	c := &fakeIGSH524Clipboard{current: clipboardSnapshot{text: "dictation", sequence: 7}}
	s := &Simulator{clipboard: c, clipSaved: true, savedClipIsText: true, savedClip: "old", lastPasted: c.current}
	if clipboardRestoreDelay < 2*time.Second || c.writes != 0 {
		t.Fatal("restore must allow target consumption before running")
	}
	s.restoreClipboard()
	if c.current.text != "old" || c.writes != 1 || s.clipSaved {
		t.Fatalf("restore failed: %+v", c)
	}
}

func TestIGSH524UnicodeFallbackSurrogatePairs(t *testing.T) {
	got := unicodeTypingEvents(" 😀")
	want := []uint16{0x20, 0xd83d, 0xde00}
	if len(got) != len(want)*2 {
		t.Fatalf("got %d events", len(got))
	}
	for i, unit := range want {
		if got[i*2].ki.wScan != unit || got[i*2].ki.dwFlags != keyeventfUnicode || got[i*2+1].ki.dwFlags != keyeventfUnicode|keyeventfKeyUp {
			t.Fatalf("incorrect Unicode event pair: %+v", got[i*2:i*2+2])
		}
	}
}
