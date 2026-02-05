package hotkey

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	MOD_ALT     = 0x0001
	MOD_CONTROL = 0x0002
	MOD_SHIFT   = 0x0004
	WM_HOTKEY   = 0x0312
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procPeekMessage      = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
)

const PM_REMOVE = 0x0001

type MSG struct {
	HWND   uintptr
	Msg    uint32
	WParam uintptr
	LParam uintptr
	Time   uint32
	Pt     struct{ X, Y int32 }
}

type Manager struct {
	hotkeys     map[int]func()
	nextID      int
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	loopStarted bool
}

func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		hotkeys: make(map[int]func()),
		nextID:  1,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// RegisterLanguageToggle registers Ctrl+Alt+Shift+L hotkey
func (m *Manager) RegisterLanguageToggle(callback func()) error {
	modifiers := uintptr(MOD_CONTROL | MOD_ALT | MOD_SHIFT)
	vkCode := uintptr(0x4C) // L key
	return m.register(modifiers, vkCode, "Ctrl+Alt+Shift+L (Language Toggle)", callback)
}

// RegisterPauseToggle registers Ctrl+Shift+P hotkey
func (m *Manager) RegisterPauseToggle(callback func()) error {
	modifiers := uintptr(MOD_CONTROL | MOD_SHIFT)
	vkCode := uintptr(0x50) // P key
	return m.register(modifiers, vkCode, "Ctrl+Shift+P (Pause/Resume)", callback)
}

func (m *Manager) register(modifiers, vkCode uintptr, description string, callback func()) error {
	m.mu.Lock()
	hotkeyID := m.nextID
	m.nextID++
	m.hotkeys[hotkeyID] = callback
	needStart := !m.loopStarted
	m.loopStarted = true
	m.mu.Unlock()

	ret, _, err := procRegisterHotKey.Call(
		0,
		uintptr(hotkeyID),
		modifiers,
		vkCode,
	)

	if ret == 0 {
		m.mu.Lock()
		delete(m.hotkeys, hotkeyID)
		m.mu.Unlock()
		return fmt.Errorf("failed to register %s: %v", description, err)
	}

	fmt.Printf("[Hotkey] Registered %s\n", description)

	// Start message loop only once
	if needStart {
		go m.messageLoop()
	}

	return nil
}

func (m *Manager) messageLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var msg MSG
	fmt.Println("[Hotkey] Message loop started on dedicated thread")

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			m.UnregisterAll()
			return
		case <-ticker.C:
			// Poll for messages using PeekMessage
			for {
				ret, _, _ := procPeekMessage.Call(
					uintptr(unsafe.Pointer(&msg)),
					0,
					0,
					0,
					PM_REMOVE,
				)

				if ret == 0 {
					// No more messages
					break
				}

				if msg.Msg == WM_HOTKEY {
					fmt.Printf("[Hotkey] WM_HOTKEY received! ID=%d\n", msg.WParam)
					hotkeyID := int(msg.WParam)
					m.mu.Lock()
					callback := m.hotkeys[hotkeyID]
					m.mu.Unlock()

					if callback != nil {
						fmt.Println("[Hotkey] Executing callback")
						go callback() // Execute in separate goroutine to avoid blocking
					} else {
						fmt.Println("[Hotkey] No callback found!")
					}
				}

				procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
				procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
			}
		}
	}
}

func (m *Manager) UnregisterAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for hotkeyID := range m.hotkeys {
		procUnregisterHotKey.Call(0, uintptr(hotkeyID))
	}
	m.hotkeys = make(map[int]func())
	fmt.Println("[Hotkey] Unregistered all hotkeys")
}

func (m *Manager) Stop() {
	m.cancel()
}
