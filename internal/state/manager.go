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
	Speaking                    // Actively transcribing (blue icon)
	MicConflict                 // Mic in use by other app (red icon)
)

func (s AppState) String() string {
	switch s {
	case Active:
		return "Active"
	case Paused:
		return "Paused"
	case AutoPaused:
		return "AutoPaused"
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
	case Speaking:
		return "blue"
	case Paused, MicConflict:
		return "red"
	default:
		return "grey"
	}
}

func (s AppState) IsTranscribing() bool {
	return s == Speaking
}

func (s AppState) IsListening() bool {
	return s == Active || s == Speaking
}

type Manager struct {
	mu    sync.RWMutex
	state AppState
}

func NewManager() *Manager {
	return &Manager{
		state: Active,
	}
}

func (m *Manager) Get() AppState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Manager) Set(newState AppState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != newState {
		fmt.Printf("[State] %s -> %s\n", m.state, newState)
		m.state = newState
	}
}

func (m *Manager) TogglePause() AppState {
	m.mu.Lock()
	defer m.mu.Unlock()

	var newState AppState
	if m.state == Paused {
		newState = Active
	} else {
		newState = Paused
	}

	fmt.Printf("[State] Toggle: %s -> %s\n", m.state, newState)
	m.state = newState
	return newState
}
