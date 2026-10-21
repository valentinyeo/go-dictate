package keyboard

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"unicode/utf16"
)

var igsh524Modifiers = [...]uint16{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0x5b, 0x5c}

type igsh524InputSink struct {
	down       map[uint16]bool
	batches    [][]input
	typed      []uint16
	modified   int
	pastes     int
	backspaces int
}

func (f *igsh524InputSink) send(events []input) (int, error) {
	f.batches = append(f.batches, append([]input(nil), events...))
	for _, event := range events {
		key := event.ki.wVk
		if key == vkControl {
			key = 0xa2
		}
		up := event.ki.dwFlags&keyeventfKeyUp != 0
		if key != 0 {
			f.down[key] = !up
		}
		if up {
			continue
		}
		if key == vkV && (f.down[0xa2] || f.down[0xa3]) {
			f.pastes++
		}
		if event.ki.dwFlags&keyeventfUnicode != 0 || key == vkBack || key == vkSpace {
			for _, modifier := range igsh524Modifiers {
				if f.down[modifier] {
					f.modified++
					break
				}
			}
			if event.ki.wScan == 'v' && (f.down[0xa2] || f.down[0xa3]) {
				f.pastes++
			}
		}
		if event.ki.dwFlags&keyeventfUnicode != 0 {
			f.typed = append(f.typed, event.ki.wScan)
		}
		if key == vkBack {
			f.backspaces++
		}
	}
	return len(events), nil
}

func TestIGSH524InputSinkDetectsUnsafeControlV(t *testing.T) {
	f := &igsh524InputSink{down: map[uint16]bool{0xa2: true}}
	f.send(unicodeTypingEvents("v"))
	f.send(pasteKeyEvents())
	if f.modified != 1 || f.pastes != 2 {
		t.Fatalf("positive control missed unsafe input: modified=%d pastes=%d", f.modified, f.pastes)
	}
}

func TestIGSH524ClipboardFailureNeverPastesWithHeldModifiers(t *testing.T) {
	for _, modifier := range igsh524Modifiers {
		t.Run(fmt.Sprintf("modifier-%x", modifier), func(t *testing.T) {
			old := clipboardSnapshot{text: "previous clipboard item", sequence: 9}
			c := &fakeIGSH524Clipboard{current: old, writeErr: fmt.Errorf("clipboard text unavailable")}
			f := &igsh524InputSink{down: map[uint16]bool{modifier: true}}
			s := &Simulator{clipboard: c, inputSink: f.send, pasteWindow: func() (uintptr, string) { return 0, "Edit" }}
			s.deliver("invented vivid text 😀")
			s.restoreTimer.Stop()
			s.restoreClipboard()
			if f.modified != 0 || f.pastes != 0 || !reflect.DeepEqual(f.typed, utf16.Encode([]rune("invented vivid text 😀"))) {
				t.Fatalf("unsafe fallback: modified=%d pastes=%d typed=%v", f.modified, f.pastes, f.typed)
			}
			if c.current != old || c.writes != 1 {
				t.Fatalf("failed write or restore changed clipboard: %+v", c)
			}
			for _, batch := range f.batches {
				for _, event := range batch {
					if event.ki.dwFlags&keyeventfKeyUp == 0 && event.ki.wVk != 0 {
						t.Fatalf("fallback emitted a virtual-key press: %+v", event)
					}
				}
			}
		})
	}
}

func TestIGSH524TypeModeAndCorrectionsClearModifiers(t *testing.T) {
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	for _, key := range igsh524Modifiers {
		f.down[key] = true
	}
	s := &Simulator{typeMode: true, inputSink: func(events []input) (int, error) {
		n, err := f.send(events)
		for _, key := range igsh524Modifiers {
			f.down[key] = true
		}
		return n, err
	}}
	s.deliver("\b\binvented vivid text")
	if f.modified != 0 || f.pastes != 0 || f.backspaces != 2 {
		t.Fatalf("unsafe correction: modified=%d pastes=%d backspaces=%d", f.modified, f.pastes, f.backspaces)
	}
}

func TestIGSH524PartialPasteReleasesKeysWithoutRetry(t *testing.T) {
	for accepted := 0; accepted < len(modifierReleaseEvents())+len(pasteKeyEvents()); accepted++ {
		t.Run(fmt.Sprintf("accepted-%d", accepted), func(t *testing.T) {
			f := &igsh524InputSink{down: make(map[uint16]bool)}
			calls := 0
			s := &Simulator{inputSink: func(events []input) (int, error) {
				calls++
				if calls == 1 {
					f.send(events[:accepted])
					return accepted, fmt.Errorf("input blocked")
				}
				for _, event := range events {
					if event.ki.dwFlags&keyeventfKeyUp == 0 {
						t.Fatalf("partial paste retried a key press: %+v", event)
					}
				}
				return f.send(events)
			}}
			s.sendInputs(pasteKeyEvents())
			if calls != 2 || f.down[0xa2] || f.down[vkV] {
				t.Fatalf("partial paste left keys held: calls=%d keys=%v", calls, f.down)
			}
		})
	}
}

func TestIGSH524CancelledDeliveryEmitsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	s := &Simulator{typeMode: true, inputSink: f.send}
	texts := make(chan string, 1)
	texts <- "invented vivid text"
	close(texts)
	s.Run(ctx, texts)
	if len(f.batches) != 0 {
		t.Fatalf("cancelled delivery emitted %d batches", len(f.batches))
	}
}

func TestIGSH524VerifiedPasteClearsUnrelatedModifiers(t *testing.T) {
	c := &fakeIGSH524Clipboard{}
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	for _, key := range igsh524Modifiers {
		f.down[key] = true
	}
	s := &Simulator{clipboard: c, inputSink: f.send, pasteWindow: func() (uintptr, string) { return 0, "Edit" }}
	s.insertText("invented vivid text")
	s.restoreTimer.Stop()
	if f.pastes != 1 || c.current.text != "invented vivid text" {
		t.Fatalf("verified paste failed: pastes=%d clipboard=%+v", f.pastes, c.current)
	}
	for i, key := range igsh524Modifiers {
		if f.down[key] || f.batches[0][i].ki.wVk != key || f.batches[0][i].ki.dwFlags != keyeventfKeyUp {
			t.Fatalf("paste inherited modifier %x", key)
		}
	}
}
