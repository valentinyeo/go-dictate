package tray

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/state"
)

var (
	user32DLL              = syscall.NewLazyDLL("user32.dll")
	procSetWindowLongPtr   = user32DLL.NewProc("SetWindowLongPtrW")
	procCallWindowProc     = user32DLL.NewProc("CallWindowProcW")
	procFindWindow         = user32DLL.NewProc("FindWindowW")
	procGetCursorPos       = user32DLL.NewProc("GetCursorPos")
)

const (
	WM_LBUTTONUP = 0x0202
	GWLP_WNDPROC = -4
)

type POINT struct {
	X, Y int32
}

var (
	originalWndProc uintptr
	globalManager   *Manager
)

// EnableLeftClick sets up a window procedure hook to capture left-clicks on the tray icon
func (m *Manager) EnableLeftClick() {
	globalManager = m

	// The systray library creates a hidden window for message processing
	// We need to find it and hook into its window procedure
	fmt.Println("[Tray] Setting up left-click handler...")

	// Note: This is a best-effort approach. The systray library doesn't officially
	// support left-click handlers, so we can't reliably hook into it without
	// modifying the library itself.

	// For now, we'll rely on the hotkey (Ctrl+Alt+X) as the primary pause method
}

// newWndProc is our custom window procedure that intercepts messages
func newWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == WM_LBUTTONUP && globalManager != nil {
		// Check if click is in tray area (approximate)
		var pt POINT
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

		// Toggle pause when clicking in tray area
		fmt.Println("[Tray] Left-click detected, toggling pause")
		newState := globalManager.stateManager.TogglePause()
		globalManager.updateIcon(newState)

		if newState == state.Paused {
			globalManager.pauseItem.SetTitle("Resume")
		} else {
			globalManager.pauseItem.SetTitle("Pause")
		}
	}

	// Call original window procedure
	ret, _, _ := procCallWindowProc.Call(
		originalWndProc,
		hwnd,
		uintptr(msg),
		wParam,
		lParam,
	)
	return ret
}
