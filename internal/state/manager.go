package state

import (
	"fmt"
	"sync"
)

type AppState int

const (
	Active      AppState = iota // Listening, VAD running (grey icon)
	Paused                      // User paused (red icon)
	AutoPaused                  // VAD silence-paused (grey icon)
	MediaPaused                 // Auto-paused due to media playback (amber icon)
	Speaking                    // Actively transcribing (blue icon)
	MicConflict                 // Mic in use by other app (red icon)
	PushToTalkIdle
	PushToTalkRecording
	PushToTalkFinishing
)

func (s AppState) String() string {
	switch s {
	case PushToTalkIdle:
		return "Push-to-talk ready: hold Ctrl+Alt+Space"
	case PushToTalkRecording:
		return "Push-to-talk recording"
	case PushToTalkFinishing:
		return "Finishing transcript (microphone off)"
	case Active:
		return "Active"
	case Paused:
		return "Paused"
	case AutoPaused:
		return "AutoPaused"
	case MediaPaused:
		return "MediaPaused"
	case Speaking:
		return "Speaking"
	case MicConflict:
		return "MicConflict"
	default:
		return "Unknown"
	}
}

func (s AppState) IconColor() string {
	switch s {
	case Active, AutoPaused:
		return "grey"
	case MediaPaused:
		return "media"
	case Speaking, PushToTalkRecording:
		return "blue"
	case Paused, MicConflict:
		return "red"
	default:
		return "grey"
	}
}

func (s AppState) IsTranscribing() bool {
	return s == Speaking || s == PushToTalkRecording
}

func (s AppState) IsListening() bool {
	return s == Active || s == Speaking || s == PushToTalkRecording
}

type Manager struct {
	mu          sync.RWMutex
	pushToTalk  bool
	state       AppState
	subscribers []chan struct{}
}

func NewManager() *Manager {
	return &Manager{
		state: Active,
	}
}

// Changed returns a NEW channel that receives a signal whenever the state
// changes. Each caller gets its own independent channel, so multiple
// goroutines can all observe state changes without stealing signals from
// each other. Call this once per goroutine and reuse the returned channel.
func (m *Manager) Changed() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan struct{}, 1)
	m.subscribers = append(m.subscribers, ch)
	return ch
}

func (m *Manager) Get() AppState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Manager) Set(newState AppState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Automatic VAD/media transitions must never reopen an idle PTT microphone.
	if m.pushToTalk && newState != Paused && newState != MicConflict {
		return
	}
	if m.state != newState {
		fmt.Printf("[State] %s -> %s\n", m.state, newState)
		m.state = newState
		m.notifyAll()
	}
}

func (m *Manager) TogglePause() AppState {
	m.mu.Lock()
	defer m.mu.Unlock()

	var newState AppState
	if m.state == Paused {
		newState = Active
		if m.pushToTalk {
			newState = PushToTalkIdle
		}
	} else {
		newState = Paused
	}

	fmt.Printf("[State] Toggle: %s -> %s\n", m.state, newState)
	m.state = newState
	m.notifyAll()
	return newState
}

// notifyAll sends a non-blocking signal to every subscriber.
// Must be called with m.mu held.
func (m *Manager) notifyAll() {
	for _, ch := range m.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// SetPushToTalk changes the capture mode without overriding a manual pause.
func (m *Manager) SetPushToTalk(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pushToTalk == enabled {
		return
	}
	m.pushToTalk = enabled
	if m.state != Paused && m.state != MicConflict {
		if enabled {
			m.state = PushToTalkIdle
		} else {
			m.state = Active
		}
	}
	m.notifyAll()
}

func (m *Manager) PushToTalkEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pushToTalk
}

// Hold ignores repeats, manual pauses, and presses while a result is finishing.
func (m *Manager) Hold(down bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pushToTalk {
		return
	}
	if down && m.state == PushToTalkIdle {
		m.state = PushToTalkRecording
	} else if !down && m.state == PushToTalkRecording {
		m.state = PushToTalkFinishing
	} else {
		return
	}
	m.notifyAll()
}

func (m *Manager) FinishPushToTalk() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pushToTalk && (m.state == PushToTalkRecording || m.state == PushToTalkFinishing) {
		m.state = PushToTalkIdle
		m.notifyAll()
	}
}

func (m *Manager) CaptureAllowed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.pushToTalk {
		return m.state == PushToTalkRecording
	}
	return m.state != Paused && m.state != MicConflict
}
