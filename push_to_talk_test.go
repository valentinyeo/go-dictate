package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/state"
)

type testEngine func(context.Context, <-chan []int16, chan<- string) error

func (f testEngine) StartStreaming(ctx context.Context, audio <-chan []int16, text chan<- string) error {
	return f(ctx, audio, text)
}

func TestPushToTalkReleaseDrainsAudioAndWaitsForFinal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	manager := state.NewManager()
	changed := manager.Changed()
	manager.SetPushToTalk(true)
	manager.Hold(true)
	manager.Hold(false)
	audio := make(chan []int16, 4)
	audio <- []int16{1, 2}
	released := false
	release := func() {
		if !released {
			audio <- []int16{3, 4}
			released = true
		}
	}
	received := make(chan []int16, 1)
	engine := testEngine(func(ctx context.Context, chunks <-chan []int16, text chan<- string) error {
		var samples []int16
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case chunk, ok := <-chunks:
				if !ok {
					received <- samples
					text <- "Final words."
					return nil
				}
				samples = append(samples, chunk...)
			}
		}
	})
	text, err := recordPushToTalk(ctx, audio, manager, changed, release, engine, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !released || manager.CaptureAllowed() {
		t.Fatal("capture remained open")
	}
	if text != "Final words." {
		t.Fatalf("text = %q", text)
	}
	if samples := <-received; !reflect.DeepEqual(samples, []int16{1, 2, 3, 4}) {
		t.Fatalf("lost tail audio: %v", samples)
	}
}

func TestPushToTalkManualPauseCancelsDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	manager := state.NewManager()
	changed := manager.Changed()
	manager.SetPushToTalk(true)
	manager.Hold(true)
	canceled := make(chan struct{})
	engine := testEngine(func(ctx context.Context, chunks <-chan []int16, text chan<- string) error {
		text <- "Do not insert this."
		manager.TogglePause()
		<-ctx.Done()
		close(canceled)
		return nil
	})
	released := false
	text, err := recordPushToTalk(ctx, make(chan []int16), manager, changed, func() { released = true }, engine, config.Default())
	if text != "" || err != nil || !released {
		t.Fatalf("pause result: %q, %v, released=%v", text, err, released)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("stream was not canceled")
	}
}
