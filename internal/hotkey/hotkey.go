package hotkey

import (
	"context"
	"fmt"
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
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
)

type MSG struct {
	HWND   uintptr
	Msg    uint32
	WParam uintptr
	LParam uintptr
	Time   uint32
	Pt     struct{ X, Y int32 }
}

type Manager struct {
	hotkeyID int
	callback func()
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		hotkeyID: 1,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Register Ctrl+Alt+Shift+L hotkey
func (m *Manager) RegisterLanguageToggle(callback func()) error {
	m.callback = callback

	// Register hotkey: Ctrl+Alt+Shift+L (VK_L = 0x4C)
	modifiers := MOD_CONTROL | MOD_ALT | MOD_SHIFT
	vkCode := uintptr(0x4C) // L key

	ret, _, err := procRegisterHotKey.Call(
		0,                   // NULL window handle
		uintptr(m.hotkeyID), // Hotkey ID
		uintptr(modifiers),
		vkCode,
	)

	if ret == 0 {
		return fmt.Errorf("failed to register hotkey: %v", err)
	}

	fmt.Println("[Hotkey] Registered Ctrl+Alt+Shift+L for language toggle")

	// Start message loop
	go m.messageLoop()

	return nil
}

func (m *Manager) messageLoop() {
	var msg MSG

	for {
		select {
		case <-m.ctx.Done():
			m.Unregister()
			return
		default:
		}

		ret, _, _ := procGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)),
			0,
			0,
			0,
		)

		if ret == 0 {
			return
		}

		if msg.Msg == WM_HOTKEY {
			if int(msg.WParam) == m.hotkeyID {
				if m.callback != nil {
					m.callback()
				}
			}
		}

		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (m *Manager) Unregister() {
	procUnregisterHotKey.Call(0, uintptr(m.hotkeyID))
	fmt.Println("[Hotkey] Unregistered hotkey")
}

func (m *Manager) Stop() {
	m.cancel()
}
