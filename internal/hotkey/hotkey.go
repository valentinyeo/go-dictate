package hotkey

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	MOD_ALT     = 0x0001
	MOD_CONTROL = 0x0002
	MOD_SHIFT   = 0x0004
	WM_HOTKEY   = 0x0312
	WM_APP      = 0x8000
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procPeekMessage        = user32.NewProc("PeekMessageW")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procGetMessage         = user32.NewProc("GetMessageW")
	procPostThreadMessage  = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

type MSG struct {
	HWND   uintptr
	Msg    uint32
	WParam uintptr
	LParam uintptr
	Time   uint32
	Pt     struct{ X, Y int32 }
}

type Registration struct {
	ID          int
	Modifiers   uintptr
	VK          uintptr
	Description string
	Callback    func()
	Result      chan error
}

type Manager struct {
	mu       sync.Mutex
	hotkeys  map[int]func()
	nextID   int
	regCh    chan Registration
	threadID uint32
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
		<-m.ready
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

	tid := atomic.LoadUint32(&m.threadID)
	procPostThreadMessage.Call(uintptr(tid), WM_APP, 0, 0)

	return <-reg.Result
}

func (m *Manager) messageLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid, _, _ := procGetCurrentThreadId.Call()
	atomic.StoreUint32(&m.threadID, uint32(tid))
	var initial MSG
	procPeekMessage.Call(uintptr(unsafe.Pointer(&initial)), 0, 0, 0, 0)
	close(m.ready)

	fmt.Println("[Hotkey] Message loop started (blocking GetMessageW, zero CPU)")

	var msg MSG
	for {
		ret, _, _ := procGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)),
			0, 0, 0,
		)
		if int32(ret) <= 0 {
			return
		}

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

func (m *Manager) RegisterHold(ctx context.Context, modifiers, vk uintptr, description string, callback func(bool)) error {
	var held atomic.Bool
	return m.Register(modifiers|0x4000, vk, description, func() {
		if !chordDown(modifiers, vk) || !held.CompareAndSwap(false, true) {
			return
		}
		defer held.Store(false)
		callback(true)
		defer callback(false)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !chordDown(modifiers, vk) {
					return
				}
			}
		}
	})
}

func keyDown(vk uintptr) bool {
	value, _, _ := procGetAsyncKeyState.Call(vk)
	return value&0x8000 != 0
}

func chordDown(modifiers, vk uintptr) bool {
	return keyDown(vk) && (modifiers&MOD_CONTROL == 0 || keyDown(0x11)) &&
		(modifiers&MOD_ALT == 0 || keyDown(0x12)) &&
		(modifiers&MOD_SHIFT == 0 || keyDown(0x10))
}

func WaitForModifiersReleased(ctx context.Context) bool {
	for keyDown(0x11) || keyDown(0x12) || keyDown(0x10) || keyDown(0x5B) || keyDown(0x5C) {
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return ctx.Err() == nil
}
