package keyboard

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
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

func (f *igsh524InputSink) modifiers() []string {
	var down []string
	for _, key := range []struct {
		left, right uint16
		name        string
	}{{0xa0, 0xa1, "Shift"}, {0xa2, 0xa3, "Ctrl"}, {0xa4, 0xa5, "Alt"}, {0x5b, 0x5b, "LWin"}, {0x5c, 0x5c, "RWin"}} {
		if f.down[key.left] || f.down[key.right] {
			down = append(down, key.name)
		}
	}
	return down
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

func TestIGSH524ClipboardFailureUsesUnicodeWithoutKeyReleases(t *testing.T) {
	old := clipboardSnapshot{text: "previous clipboard item", sequence: 9}
	c := &fakeIGSH524Clipboard{current: old, writeErr: fmt.Errorf("clipboard text unavailable")}
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	s := &Simulator{clipboard: c, inputSink: f.send, modifierState: f.modifiers, pasteWindow: func() (uintptr, string) { return 0, "Edit" }}
	s.deliver(context.Background(), "invented vivid text 😀")
	s.restoreTimer.Stop()
	s.restoreClipboard()
	want := unicodeTypingEvents("invented vivid text 😀")
	if len(f.batches) != 1 || !reflect.DeepEqual(f.batches[0], want) || f.modified != 0 || f.pastes != 0 {
		t.Fatalf("unsafe fallback: batches=%v modified=%d pastes=%d", f.batches, f.modified, f.pastes)
	}
	if c.current != old || c.writes != 1 {
		t.Fatalf("failed write or restore changed clipboard: %+v", c)
	}
}

func TestIGSH524NoUnsolicitedModifierReleases(t *testing.T) {
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	s := &Simulator{typeMode: true, inputSink: f.send, modifierState: f.modifiers}
	s.deliver(context.Background(), "\b\binvented vivid text")
	want := [][]input{backspaceEvents(2), typingEvents("invented vivid text")}
	if !reflect.DeepEqual(f.batches, want) || f.modified != 0 || f.pastes != 0 || f.backspaces != 2 {
		t.Fatalf("unexpected input: batches=%v modified=%d pastes=%d backspaces=%d", f.batches, f.modified, f.pastes, f.backspaces)
	}
}

func TestIGSH524HeldModifiersEmitNothing(t *testing.T) {
	for _, modifier := range igsh524Modifiers {
		for _, typeMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("modifier-%x-type-%t", modifier, typeMode), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f := &igsh524InputSink{down: map[uint16]bool{modifier: true}}
				c := &fakeIGSH524Clipboard{}
				waits := 0
				s := &Simulator{typeMode: typeMode, clipboard: c, inputSink: f.send, modifierState: f.modifiers, modifierWait: func(waitCtx context.Context) bool {
					waits++
					deadline, ok := waitCtx.Deadline()
					if !ok || time.Until(deadline) > modifierWaitLimit {
						t.Fatal("modifier wait is not bounded")
					}
					if len(f.batches) != 0 || c.writes != 0 || !f.down[modifier] {
						t.Fatal("held modifier was changed or payload dispatched before release")
					}
					if waits == 2 {
						cancel()
					}
					return false
				}}
				s.deliver(ctx, "\bvivid")
				if waits != 2 || len(f.batches) != 0 || c.writes != 0 || !f.down[modifier] {
					t.Fatalf("held input escaped: waits=%d batches=%v clipboard=%+v down=%v", waits, f.batches, c, f.down)
				}
			})
		}
	}
}

func TestIGSH524HeldControlPayloadTypesOnlyAfterRelease(t *testing.T) {
	f := &igsh524InputSink{down: map[uint16]bool{0xa2: true}}
	c := &fakeIGSH524Clipboard{}
	waits := 0
	s := &Simulator{clipboard: c, inputSink: f.send, modifierState: f.modifiers, modifierWait: func(context.Context) bool {
		waits++
		if len(f.batches) != 0 || c.writes != 0 {
			t.Fatal("Ctrl+V or typing was emitted while Ctrl remained held")
		}
		if waits == 2 {
			f.down[0xa2] = false
			return true
		}
		return false
	}}
	s.deliver(context.Background(), "\b\bvivid 😀\bview")
	if waits != 2 || f.modified != 0 || f.pastes != 0 || f.backspaces != 3 || c.writes != 0 || !reflect.DeepEqual(f.typed, utf16.Encode([]rune("vivid 😀view"))) {
		t.Fatalf("unsafe or incomplete released payload: waits=%d modified=%d pastes=%d typed=%v backspaces=%d writes=%d", waits, f.modified, f.pastes, f.typed, f.backspaces, c.writes)
	}
}

func TestIGSH524PartialInputResumesAllEvents(t *testing.T) {
	for _, events := range [][]input{typingEvents("vivid text"), unicodeTypingEvents("vivid 😀"), backspaceEvents(3), pasteKeyEvents()} {
		for accepted := 0; accepted < len(events); accepted++ {
			t.Run(fmt.Sprintf("events-%d-accepted-%d", len(events), accepted), func(t *testing.T) {
				f := &igsh524InputSink{down: make(map[uint16]bool)}
				calls := 0
				s := &Simulator{modifierState: f.modifiers, inputSink: func(remaining []input) (int, error) {
					calls++
					if calls == 1 {
						if accepted > 0 {
							f.send(remaining[:accepted])
						}
						return accepted, fmt.Errorf("input contention")
					}
					if !reflect.DeepEqual(remaining, events[accepted:]) {
						t.Fatalf("did not resume from offset: got=%v want=%v", remaining, events[accepted:])
					}
					return f.send(remaining)
				}}
				s.sendInputs(context.Background(), events)
				var delivered []input
				for _, batch := range f.batches {
					delivered = append(delivered, batch...)
				}
				if calls != 2 || !reflect.DeepEqual(delivered, events) {
					t.Fatalf("partial insertion lost or duplicated events: calls=%d got=%v want=%v", calls, delivered, events)
				}
			})
		}
	}
}

func TestIGSH524ProgressResetsBlockedRetryBudget(t *testing.T) {
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	events := unicodeTypingEvents("vivid text")
	calls, offset := 0, 0
	s := &Simulator{inputSink: func(remaining []input) (int, error) {
		calls++
		if !reflect.DeepEqual(remaining, events[offset:]) {
			t.Fatalf("retry lost its offset: got=%v want=%v", remaining, events[offset:])
		}
		if calls%2 == 1 {
			return 0, fmt.Errorf("input contention")
		}
		f.send(remaining[:1])
		offset++
		return 1, nil
	}}
	s.sendInputs(context.Background(), events)
	if calls != len(events)*2 || offset != len(events) || !reflect.DeepEqual(f.typed, utf16.Encode([]rune("vivid text"))) {
		t.Fatalf("progress exhausted retry budget: calls=%d offset=%d typed=%v", calls, offset, f.typed)
	}
}

func TestIGSH524ControlPressedDuringClipboardWorkNeverPastes(t *testing.T) {
	for _, writeFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("write-fails-%t", writeFails), func(t *testing.T) {
			f := &igsh524InputSink{down: make(map[uint16]bool)}
			c := &fakeIGSH524Clipboard{onRead: func(c *fakeIGSH524Clipboard) {
				if (writeFails && c.reads == 1) || (!writeFails && c.reads == 3) {
					f.down[0xa2] = true
				}
			}}
			if writeFails {
				c.writeErr = fmt.Errorf("clipboard unavailable")
			}
			waits := 0
			s := &Simulator{clipboard: c, inputSink: f.send, modifierState: f.modifiers, pasteWindow: func() (uintptr, string) { return 0, "Edit" }, modifierWait: func(context.Context) bool {
				waits++
				if len(f.batches) != 0 || !f.down[0xa2] {
					t.Fatal("input or synthetic release emitted while Ctrl was held")
				}
				f.down[0xa2] = false
				return true
			}}
			s.deliver(context.Background(), "vivid\bview")
			s.restoreTimer.Stop()
			if waits != 1 || f.modified != 0 || f.pastes != 0 || c.writes != 1 || f.backspaces != 1 || !reflect.DeepEqual(f.typed, utf16.Encode([]rune("vividview"))) {
				t.Fatalf("unsafe delayed fallback: waits=%d modified=%d pastes=%d typed=%v writes=%d", waits, f.modified, f.pastes, f.typed, c.writes)
			}
		})
	}
}

func TestIGSH524ExhaustedInputCleansOnlyInjectedDowns(t *testing.T) {
	for _, accepted := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("accepted-%d", accepted), func(t *testing.T) {
			f := &igsh524InputSink{down: make(map[uint16]bool)}
			events := pasteKeyEvents()
			calls := 0
			s := &Simulator{modifierState: f.modifiers, inputSink: func(remaining []input) (int, error) {
				calls++
				if calls == 1 && accepted > 0 {
					f.send(remaining[:accepted])
					return accepted, nil
				}
				if calls <= 10 || (accepted > 0 && calls == 11) {
					if !reflect.DeepEqual(remaining, events[accepted:]) {
						t.Fatalf("retry changed remaining events: %v", remaining)
					}
					return 0, fmt.Errorf("input blocked")
				}
				for _, event := range remaining {
					if event.ki.dwFlags&keyeventfKeyUp == 0 || (event.ki.wVk != vkControl && event.ki.wVk != vkV) {
						t.Fatalf("cleanup released an uninjected key: %+v", event)
					}
				}
				return f.send(remaining)
			}}
			s.sendInputs(context.Background(), events)
			wantCalls := 12
			if accepted == 0 {
				wantCalls = 10
			}
			if calls != wantCalls || f.down[0xa2] || f.down[vkV] || f.pastes > 1 {
				t.Fatalf("unbounded retry or stuck keys: calls=%d keys=%v pastes=%d", calls, f.down, f.pastes)
			}
		})
	}
}

func TestIGSH524CancelledDeliveryEmitsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	s := &Simulator{typeMode: true, inputSink: f.send, modifierState: f.modifiers}
	texts := make(chan string, 1)
	texts <- "invented vivid text"
	close(texts)
	s.Run(ctx, texts)
	if len(f.batches) != 0 {
		t.Fatalf("cancelled delivery emitted %d batches", len(f.batches))
	}
}

func TestIGSH524VerifiedPasteHasNoUnsolicitedKeyReleases(t *testing.T) {
	c := &fakeIGSH524Clipboard{}
	f := &igsh524InputSink{down: make(map[uint16]bool)}
	s := &Simulator{clipboard: c, inputSink: f.send, modifierState: f.modifiers, pasteWindow: func() (uintptr, string) { return 0, "Edit" }}
	s.deliver(context.Background(), "invented vivid text")
	s.restoreTimer.Stop()
	if f.pastes != 1 || c.current.text != "invented vivid text" || len(f.batches) != 1 || !reflect.DeepEqual(f.batches[0], pasteKeyEvents()) {
		t.Fatalf("verified paste failed or emitted extra keys: pastes=%d clipboard=%+v batches=%v", f.pastes, c.current, f.batches)
	}
}
