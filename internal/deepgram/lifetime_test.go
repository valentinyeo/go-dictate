package deepgram

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valen/go-dictate/internal/audio"
)

type streamTimer interface {
	Stop() bool
}

type lifetimeClock struct {
	now    time.Time
	timers []*lifetimeTimer
}

type lifetimeTimer struct {
	at      time.Time
	f       func()
	stopped bool
}

func (t *lifetimeTimer) Stop() bool {
	active := !t.stopped
	t.stopped = true
	return active
}

func (c *lifetimeClock) afterFunc(delay time.Duration, f func()) streamTimer {
	timer := &lifetimeTimer{at: c.now.Add(delay), f: f}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *lifetimeClock) advance(delay time.Duration) {
	until := c.now.Add(delay)
	for {
		var next *lifetimeTimer
		for _, timer := range c.timers {
			if !timer.stopped && !timer.at.After(until) && (next == nil || timer.at.Before(next.at)) {
				next = timer
			}
		}
		if next == nil {
			break
		}
		c.now = next.at
		next.stopped = true
		next.f()
	}
	c.now = until
}

type lifetimeSocket struct {
	closed     bool
	closes     int
	keepAlives int
	audio      int
}

func (s *lifetimeSocket) WriteMessage(kind int, data []byte) error {
	if s.closed {
		return net.ErrClosed
	}
	if kind == websocket.TextMessage && string(data) == `{"type":"KeepAlive"}` {
		s.keepAlives++
	}
	if kind == websocket.BinaryMessage {
		s.audio++
	}
	return nil
}

func (s *lifetimeSocket) Close() error {
	s.closed = true
	s.closes++
	return nil
}

func newLifetimeFixture(t *testing.T) (*StreamLifetime, *lifetimeClock, *streamSession, *lifetimeSocket) {
	t.Helper()
	clock := &lifetimeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	lifetime := NewStreamLifetime(true)
	lifetime.now = func() time.Time { return clock.now }
	client := NewClient("test", "en", "nova-3", false, false, false, false, nil, true)
	client.SetStreamLifetime(context.Background(), lifetime)
	session := client.session
	socket := &lifetimeSocket{}
	if !session.attach(socket) {
		t.Fatal("new speech could not attach its socket")
	}
	t.Cleanup(func() { lifetime.Close("test_cleanup") })
	return lifetime, clock, session, socket
}

func TestIGSH570MicReleaseThenWatchdogRestarts(t *testing.T) {
	lifetime, clock, session, socket := newLifetimeFixture(t)
	lifetime.Speech()
	lifetime.SetMicrophone(false)
	if !socket.closed || session.ctx.Err() == nil || session.reason != "mic_released" {
		t.Fatal("microphone release did not immediately close the active speech connection")
	}
	lastSpeech := session.lastSpeech
	for second := 1; second <= 60; second++ {
		clock.advance(time.Second)
		lifetime.Close("settings_changed")
		if second%3 == 0 {
			if err := session.writeMessage(websocket.TextMessage, []byte(`{"type":"KeepAlive"}`)); !errors.Is(err, context.Canceled) {
				t.Fatalf("KeepAlive after release: %v", err)
			}
		}
		if lifetime.Active() || !socket.closed || session.lastSpeech != lastSpeech {
			t.Fatalf("watchdog restart at %ds revived or refreshed the connection", second)
		}
	}
	if socket.keepAlives != 0 || socket.closes != 1 {
		t.Fatalf("after release: KeepAlives=%d closes=%d", socket.keepAlives, socket.closes)
	}
	lifetime.SetMicrophone(true)
	clock.advance(time.Minute)
	if lifetime.Active() || socket.closes != 1 || socket.keepAlives != 0 {
		t.Fatal("reopening capture without speech revived the socket")
	}
}

func TestIGSH570LongSilenceKeepsSocketWithoutAudio(t *testing.T) {
	lifetime, clock, session, socket := newLifetimeFixture(t)
	const chunkMs = 80
	gate := audio.NewStreamGate((1500+chunkMs-1)/chunkMs, 3)
	gate.Start(1)
	for i := 0; i < (1500+chunkMs-1)/chunkMs; i++ {
		for range gate.Next([]int16{0}, false) {
			if err := session.writeMessage(websocket.BinaryMessage, []byte{0, 0}); err != nil {
				t.Fatal(err)
			}
		}
	}
	audioBefore := socket.audio
	for i := 1; i <= 10*60*1000/chunkMs; i++ {
		clock.advance(chunkMs * time.Millisecond)
		for range gate.Next([]int16{0}, false) {
			if err := session.writeMessage(websocket.BinaryMessage, []byte{0, 0}); err != nil {
				t.Fatalf("silence at chunk %d: %v", i, err)
			}
		}
		if i%(3000/chunkMs) == 0 {
			if err := session.writeMessage(websocket.TextMessage, []byte(`{"type":"KeepAlive"}`)); err != nil {
				t.Fatalf("KeepAlive at chunk %d: %v", i, err)
			}
		}
		if !lifetime.Active() || socket.closed || session.reason != "" {
			t.Fatalf("silence closed the session at chunk %d: %s", i, session.reason)
		}
	}
	if socket.audio != audioBefore || socket.keepAlives == 0 || socket.closes != 0 {
		t.Fatalf("silent gap: audio=%d want=%d KeepAlives=%d closes=%d", socket.audio, audioBefore, socket.keepAlives, socket.closes)
	}
}

func TestIGSH570TenMinutesContinuousSpeech(t *testing.T) {
	lifetime, clock, session, socket := newLifetimeFixture(t)
	for tick := 1; tick <= 10*60*10; tick++ {
		clock.afterFunc(100*time.Millisecond, func() {
			lifetime.Speech()
			if err := session.writeMessage(websocket.BinaryMessage, []byte{1, 0}); err != nil {
				t.Fatalf("speech at tick %d: %v", tick, err)
			}
		})
		clock.advance(100 * time.Millisecond)
		if !lifetime.Active() || socket.closed || session.reason != "" || lifetime.current != session {
			t.Fatalf("continuous speech interrupted at tick %d: %s", tick, session.reason)
		}
	}
	if socket.audio != 6000 || socket.closes != 0 {
		t.Fatalf("continuous speech: audio=%d closes=%d", socket.audio, socket.closes)
	}
}

func TestIGSH570MaxAgeOnlyAtNewUtterance(t *testing.T) {
	lifetime, clock, session, socket := newLifetimeFixture(t)
	for tick := 1; tick <= 900; tick++ {
		clock.advance(100 * time.Millisecond)
		lifetime.Speech()
		if err := session.writeMessage(websocket.BinaryMessage, []byte{1, 0}); err != nil {
			t.Fatalf("speech before new utterance at tick %d: %v", tick, err)
		}
	}
	if !lifetime.BeginUtterance() {
		t.Fatal("exactly 90 seconds should still allow reuse, as in v1.8.116")
	}
	clock.advance(time.Millisecond)
	lifetime.Speech()
	if err := session.writeMessage(websocket.BinaryMessage, []byte{1, 0}); err != nil {
		t.Fatalf("ongoing speech past 90 seconds: %v", err)
	}
	if socket.closed || session.reason != "" || !lifetime.Active() {
		t.Fatal("age was enforced before a new utterance")
	}
	if lifetime.BeginUtterance() || !socket.closed || socket.closes != 1 || session.reason != "max_age" || session.ctx.Err() == nil {
		t.Fatal("new utterance did not close the old connection with reason=max_age")
	}
	if err := session.writeMessage(websocket.BinaryMessage, []byte{1, 0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired session write: %v", err)
	}
	client := NewClient("test", "en", "nova-3", false, false, false, false, nil, true)
	client.SetStreamLifetime(context.Background(), lifetime)
	fresh := &lifetimeSocket{}
	if client.session == session || !client.session.attach(fresh) || !lifetime.BeginUtterance() {
		t.Fatal("new utterance could not reconnect")
	}
	if err := client.session.writeMessage(websocket.BinaryMessage, []byte{1, 0}); err != nil {
		t.Fatalf("fresh session write: %v", err)
	}
	session.close("stream_ended")
	if fresh.closed || socket.closes != 1 {
		t.Fatal("old session completion closed the replacement")
	}
}

func TestPersistentResetDoesNotRefreshOrReopen(t *testing.T) {
	lifetime, clock, session, socket := newLifetimeFixture(t)
	clock.advance(29 * time.Second)
	lastSpeech := session.lastSpeech
	started := session.started
	lifetime.Close("settings_changed")
	for restart := 0; restart < 12; restart++ {
		clock.advance(5 * time.Second)
		lifetime.Close("settings_changed")
		if lifetime.Active() || session.lastSpeech != lastSpeech || session.started != started {
			t.Fatal("reset reopened the socket or reset a deadline")
		}
	}
	if !socket.closed || socket.closes != 1 || session.reason != "settings_changed" {
		t.Fatal("reset did not close exactly once")
	}
}

func TestPersistentReleaseWhileDialingCannotAttachAfterResume(t *testing.T) {
	lifetime, _, _, oldSocket := newLifetimeFixture(t)
	oldSession := lifetime.begin(context.Background())
	lifetime.SetMicrophone(false)
	lateSocket := &lifetimeSocket{}
	if oldSession.attach(lateSocket) || !lateSocket.closed {
		t.Fatal("a late dial attached after release")
	}
	lifetime.SetMicrophone(true)
	anotherLateSocket := &lifetimeSocket{}
	if oldSession.attach(anotherLateSocket) || !anotherLateSocket.closed {
		t.Fatal("resuming capture revived a cancelled dial")
	}
	client := NewClient("test", "en", "nova-3", false, false, false, false, nil, true)
	client.SetStreamLifetime(context.Background(), lifetime)
	newSocket := &lifetimeSocket{}
	if !client.session.attach(newSocket) {
		t.Fatal("fresh speech could not open a new socket")
	}
	oldSession.close("stream_ended")
	if newSocket.closed || !oldSocket.closed || !lifetime.Active() {
		t.Fatal("old connection completion affected the new speech session")
	}
}

func TestPersistentReleasedMicRejectsNewSession(t *testing.T) {
	lifetime, _, _, _ := newLifetimeFixture(t)
	lifetime.SetMicrophone(false)
	client := NewClient("test", "en", "nova-3", false, false, false, false, nil, true)
	client.SetStreamLifetime(context.Background(), lifetime)
	socket := &lifetimeSocket{}
	if client.session.ctx.Err() == nil || client.session.attach(socket) || !socket.closed {
		t.Fatal("queued speech opened a socket while the microphone was released")
	}
}

type blockedLifetimeSocket struct {
	writing chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (s *blockedLifetimeSocket) WriteMessage(int, []byte) error {
	close(s.writing)
	<-s.closed
	return net.ErrClosed
}

func (s *blockedLifetimeSocket) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestPersistentReleaseUnblocksSocketWrite(t *testing.T) {
	lifetime, _, _, _ := newLifetimeFixture(t)
	session := lifetime.begin(context.Background())
	socket := &blockedLifetimeSocket{writing: make(chan struct{}), closed: make(chan struct{})}
	t.Cleanup(func() { socket.Close() })
	if !session.attach(socket) {
		t.Fatal("could not attach socket")
	}
	written := make(chan error, 1)
	go func() { written <- session.writeMessage(websocket.BinaryMessage, []byte{1, 0}) }()
	select {
	case <-socket.writing:
	case <-time.After(time.Second):
		t.Fatal("socket write did not start")
	}
	released := make(chan struct{})
	go func() {
		lifetime.SetMicrophone(false)
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(time.Second):
		socket.Close()
		t.Fatal("release waited for a blocked socket write")
	}
	select {
	case err := <-written:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked write returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("socket close did not unblock the writer")
	}
}

func TestIGSH570SlowDialHasNoIdleDeadline(t *testing.T) {
	lifetime, clock, _, _ := newLifetimeFixture(t)
	session := lifetime.begin(context.Background())
	clock.advance(time.Minute)
	socket := &lifetimeSocket{}
	if !session.attach(socket) || socket.closed {
		t.Fatal("silence during dial closed the session")
	}
	clock.advance(time.Minute)
	if err := session.writeMessage(websocket.BinaryMessage, []byte{1, 0}); err != nil {
		t.Fatalf("first utterance after slow dial: %v", err)
	}
	if socket.closed || session.reason != "" {
		t.Fatal("dial introduced an idle or mid-utterance age deadline")
	}
}
