package deepgram

import (
	"context"
	"sync"
	"time"

	"github.com/valen/go-dictate/internal/logger"
)

const persistentMaxAge = 90 * time.Second

type streamSocket interface {
	WriteMessage(int, []byte) error
	Close() error
}

type StreamLifetime struct {
	mu         sync.Mutex
	now        func() time.Time
	microphone bool
	current    *streamSession
}

type streamSession struct {
	owner      *StreamLifetime
	ctx        context.Context
	cancel     context.CancelFunc
	started    time.Time
	lastSpeech time.Time
	socket     streamSocket
	reason     string
}

func NewStreamLifetime(microphone bool) *StreamLifetime {
	return &StreamLifetime{
		now:        time.Now,
		microphone: microphone,
	}
}

func (l *StreamLifetime) SetMicrophone(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.microphone = enabled
	if !enabled && l.current != nil {
		l.current.closeLocked("mic_released")
	}
}

func (l *StreamLifetime) Close(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != nil {
		l.current.closeLocked(reason)
	}
}

func (l *StreamLifetime) Speech() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != nil && l.current.activeLocked() {
		l.current.lastSpeech = l.now()
	}
}

func (l *StreamLifetime) Active() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current != nil && l.current.activeLocked()
}

func (l *StreamLifetime) BeginUtterance() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current == nil || !l.current.activeLocked() {
		return false
	}
	if l.now().Sub(l.current.started) > persistentMaxAge {
		l.current.closeLocked("max_age")
		return false
	}
	return true
}

func (l *StreamLifetime) begin(ctx context.Context) *streamSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != nil {
		l.current.closeLocked("replaced")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	now := l.now()
	s := &streamSession{owner: l, ctx: streamCtx, cancel: cancel, started: now, lastSpeech: now}
	l.current = s
	if !l.microphone {
		s.closeLocked("mic_released")
	}
	return s
}

func (s *streamSession) attach(socket streamSocket) bool {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if !s.activeLocked() {
		socket.Close()
		return false
	}
	s.socket = socket
	return true
}

func (s *streamSession) activeLocked() bool {
	if s.reason != "" {
		return false
	}
	if s.ctx.Err() != nil {
		s.closeLocked("cancelled")
	}
	return s.reason == ""
}

func (s *streamSession) writeMessage(kind int, data []byte) error {
	s.owner.mu.Lock()
	active := s.activeLocked()
	socket := s.socket
	s.owner.mu.Unlock()
	if !active || socket == nil {
		return context.Canceled
	}
	return socket.WriteMessage(kind, data)
}

func (s *streamSession) close(reason string) {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.closeLocked(reason)
}

func (s *streamSession) closeLocked(reason string) {
	if s.reason != "" {
		return
	}
	s.reason = reason
	s.cancel()
	if s.socket != nil {
		s.socket.Close()
	}
	logger.Info("[STT] Closed persistent connection reason=%s age=%s", reason, s.owner.now().Sub(s.started).Round(time.Millisecond))
}
