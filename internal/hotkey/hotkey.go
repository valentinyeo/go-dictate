package hotkey

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	MOD_ALT     = 0x0001
	MOD_CONTROL = 0x0002
	MOD_SHIFT   = 0x0004
	WM_HOTKEY   = 0x0312
	WM_APP      = 0x8000 // custom message to wake up GetMessageW
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procGetMessage          = user32.NewProc("GetMessageW")
	procPostThreadMessage   = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
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
	mu       sync.Mutex
	hotkeys  map[int]func()
	nextID   int
	regCh    chan Registration
	threadID uint32 // set by message loop goroutine
	ready    chan struct{}
	started  bool
}

func NewManager() *Manager {
	return &Manager{
		hotkeys: make(map[int]func()),
		nextID:  1,
		regCh:   make(chan Registration, 8),
		ready:   make(chan struct{}),
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
		<-m.ready // wait for loop thread to be ready
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

	// Wake up GetMessageW so it drains the registration channel
	tid := atomic.LoadUint32(&m.threadID)
	procPostThreadMessage.Call(uintptr(tid), WM_APP, 0, 0)

	return <-reg.Result
}

func (m *Manager) messageLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Store thread ID so Register can post messages to wake us up
	tid, _, _ := procGetCurrentThreadId.Call()
	atomic.StoreUint32(&m.threadID, uint32(tid))
	close(m.ready)

	fmt.Println("[Hotkey] Message loop started (blocking GetMessageW, zero CPU)")

	var msg MSG
	for {
		// GetMessageW blocks until a message is available — zero CPU while waiting.
		ret, _, _ := procGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)),
			0, 0, 0,
		)
		if int32(ret) <= 0 {
			return // WM_QUIT or error
		}

		// Drain any pending registrations
		m.drainRegistrations()

		if msg.Msg == WM_HOTKEY {
			hotkeyID := int(msg.WParam)
			m.mu.Lock()
			cb := m.hotkeys[hotkeyID]
			m.mu.Unlock()
			if cb != nil {
				go cb()
			}
		}
	}
}

func (m *Manager) drainRegistrations() {
	for {
		select {
		case reg := <-m.regCh:
			ret, _, err := procRegisterHotKey.Call(0, uintptr(reg.ID), reg.Modifiers, reg.VK)
			if ret == 0 {
				reg.Result <- fmt.Errorf("failed to register %s: %v", reg.Description, err)
				continue
			}
			m.mu.Lock()
			m.hotkeys[reg.ID] = reg.Callback
			m.mu.Unlock()
			fmt.Printf("[Hotkey] Registered %s\n", reg.Description)
			reg.Result <- nil
		default:
			return
		}
	}
}
