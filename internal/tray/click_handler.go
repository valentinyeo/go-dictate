package tray

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/state"
)

var (
	user32DLL            = syscall.NewLazyDLL("user32.dll")
	procSetWindowLongPtr = user32DLL.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32DLL.NewProc("CallWindowProcW")
	procFindWindow       = user32DLL.NewProc("FindWindowW")
	procPostMessage      = user32DLL.NewProc("PostMessageW")
)

const (
	wmNull      = 0x0000
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmUser      = 0x0400
)

// GWLP_WNDPROC = -4, expressed as uintptr (two's complement)
var gwlpWndProc = ^uintptr(3)

var (
	originalWndProc uintptr
	clickManager    *Manager

	// Create callback ONCE at init - syscall.NewCallback has a global ~2000 limit.
	subclassedWndProc = syscall.NewCallback(trayWndProc)
)

// EnableLeftClick subclasses the systray hidden window to:
//   - Toggle pause on left-click (instead of showing the menu)
//   - Fix right-click menu reliability (PostMessage WM_NULL after TrackPopupMenu)
//
// The getlantern/systray library is missing the PostMessage(WM_NULL) call after
// TrackPopupMenu, which is required by Microsoft KB Q135788. Without it, the
// popup menu fails to appear on subsequent right-clicks because Windows doesn't
// properly release the foreground lock.
func (m *Manager) EnableLeftClick() {
	clickManager = m

	className, _ := syscall.UTF16PtrFromString("SystrayClass")
	hwnd, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd == 0 {
		fmt.Println("[Tray] WARNING: Could not find systray window for click handler")
		return
	}

	oldProc, _, _ := procSetWindowLongPtr.Call(
		hwnd,
		uintptr(gwlpWndProc),
		subclassedWndProc,
	)
	if oldProc == 0 {
		fmt.Println("[Tray] WARNING: Failed to install click handler")
		return
	}
	originalWndProc = oldProc
	fmt.Println("[Tray] Click handler installed (left-click=toggle, right-click menu fix)")
}

// trayWndProc is our subclassed window procedure that intercepts tray icon messages.
func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	const wmSystrayMessage = wmUser + 1

	if msg == wmSystrayMessage {
		switch lParam {
		case wmLButtonUp:
			// Left-click: toggle pause instead of showing the context menu.
			if clickManager != nil {
				newState := clickManager.stateManager.TogglePause()
				clickManager.updateIcon(newState)
				if newState == state.Paused {
					clickManager.pauseItem.SetTitle("Resume")
				} else {
					clickManager.pauseItem.SetTitle("Pause")
				}
			}
			return 0

		case wmRButtonUp:
			// Right-click: let the original handler call TrackPopupMenu (blocking),
			// then post WM_NULL to fix the foreground-lock bug (KB Q135788).
			ret, _, _ := procCallWindowProc.Call(
				originalWndProc, hwnd, uintptr(msg), wParam, lParam,
			)
			procPostMessage.Call(hwnd, wmNull, 0, 0)
			return ret
		}
	}

	// Everything else: pass through to the original wndProc.
	ret, _, _ := procCallWindowProc.Call(
		originalWndProc, hwnd, uintptr(msg), wParam, lParam,
	)
	return ret
}
