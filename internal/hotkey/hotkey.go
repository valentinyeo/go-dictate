package hotkey

import (
	"fmt"
	"runtime"
	"sync"
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
	procGetMessage       = user32.NewProc("GetMessageW")
)

type MSG struct {
	HWND   uintptr
	Msg    uint32
	WParam uintptr
	LParam uintptr
	Time   uint32
	Pt     struct{ X, Y int32 }
}

// Registration holds a pending hotkey to register on the message loop thread.
type Registration struct {
	ID          int
	Modifiers   uintptr
	VK          uintptr
	Description string
	Callback    func()
	Result      chan error
}

// Manager runs a single OS-thread-locked message loop using blocking
// GetMessageW. Zero CPU when idle — the thread sleeps in the kernel
// until a WM_HOTKEY arrives.
type Manager struct {
	mu      sync.Mutex
	hotkeys map[int]func()
	nextID  int
	regCh   chan Registration // send registrations to the message loop thread
	started bool
}

func NewManager() *Manager {
	return &Manager{
		hotkeys: make(map[int]func()),
		nextID:  1,
		regCh:   make(chan Registration, 8),
	}
}

func (m *Manager) Register(modifiers, vkCode uintptr, description string, callback func()) error {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	needStart := !m.started
	m.started = true
	m.mu.Unlock()

	if needStart {
		go m.messageLoop()
	}

	reg := Registration{
		ID:          id,
		Modifiers:   modifiers,
		VK:          vkCode,
		Description: description,
		Callback:    callback,
		Result:      make(chan error, 1),
	}
	m.regCh <- reg
	return <-reg.Result
}

func (m *Manager) messageLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fmt.Println("[Hotkey] Message loop started (blocking GetMessageW)")

	// Process any pending registrations before entering the blocking loop.
	// GetMessageW blocks until a message arrives, so we need to register
	// hotkeys first, otherwise we'd block forever with nothing registered.
	for {
		select {
		case reg := <-m.regCh:
			m.doRegister(reg)
		default:
			goto loop
		}
	}

loop:
	var msg MSG
	for {
		// GetMessageW blocks until a message is available — zero CPU while waiting.
		// Returns 0 for WM_QUIT, -1 for error.
		ret, _, _ := procGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)),
			0, 0, 0,
		)
		if int32(ret) <= 0 {
			return // WM_QUIT or error
		}

		if msg.Msg == WM_HOTKEY {
			hotkeyID := int(msg.WParam)
			m.mu.Lock()
			cb := m.hotkeys[hotkeyID]
			m.mu.Unlock()
			if cb != nil {
				go cb()
			}
		}

		// Also drain any pending registrations (PostThreadMessage wakes GetMessageW)
		for {
			select {
			case reg := <-m.regCh:
				m.doRegister(reg)
			default:
				goto done
			}
		}
	done:
	}
}

func (m *Manager) doRegister(reg Registration) {
	ret, _, err := procRegisterHotKey.Call(0, uintptr(reg.ID), reg.Modifiers, reg.VK)
	if ret == 0 {
		reg.Result <- fmt.Errorf("failed to register %s: %v", reg.Description, err)
		return
	}
	m.mu.Lock()
	m.hotkeys[reg.ID] = reg.Callback
	m.mu.Unlock()
	fmt.Printf("[Hotkey] Registered %s\n", reg.Description)
	reg.Result <- nil
}
