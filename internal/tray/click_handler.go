package tray

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/state"
)

var (
	user32DLL            = syscall.NewLazyDLL("user32.dll")
	kernel32Click        = syscall.NewLazyDLL("kernel32.dll")
	procSetWindowLongPtr = user32DLL.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32DLL.NewProc("CallWindowProcW")
	procFindWindow       = user32DLL.NewProc("FindWindowW")
	procPostMessage      = user32DLL.NewProc("PostMessageW")

	// For forcing foreground window when another app has focus
	procGetForegroundWindow      = user32DLL.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32DLL.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32DLL.NewProc("AttachThreadInput")
	procSetForegroundWindow      = user32DLL.NewProc("SetForegroundWindow")
	procGetCurrentThreadId       = kernel32Click.NewProc("GetCurrentThreadId")
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
	systrayHwnd     uintptr

	// Create callback ONCE at init - syscall.NewCallback has a global ~2000 limit.
	subclassedWndProc = syscall.NewCallback(trayWndProc)
)

// EnableLeftClick subclasses the systray hidden window to:
//   - Toggle pause on left-click (instead of showing the menu)
//   - Fix right-click menu reliability (PostMessage WM_NULL + AttachThreadInput)
func (m *Manager) EnableLeftClick() {
	clickManager = m

	className, _ := syscall.UTF16PtrFromString("SystrayClass")
	hwnd, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd == 0 {
		fmt.Println("[Tray] WARNING: Could not find systray window for click handler")
		return
	}
	systrayHwnd = hwnd

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

// forceforeground temporarily attaches our thread's input queue to the
// foreground window's thread, making SetForegroundWindow work even when
// another app (like Teams, Chrome) has focus. This is the standard Windows
// workaround for the foreground lock.
func forceForeground() {
	fgHwnd, _, _ := procGetForegroundWindow.Call()
	if fgHwnd == 0 || fgHwnd == systrayHwnd {
		return // Already foreground or no foreground window
	}

	fgTID, _, _ := procGetWindowThreadProcessId.Call(fgHwnd, 0)
	ourTID, _, _ := procGetCurrentThreadId.Call()
	if fgTID == ourTID || fgTID == 0 {
		return
	}

	// Attach to foreground thread's input queue so SetForegroundWindow works
	procAttachThreadInput.Call(ourTID, fgTID, 1) // TRUE = attach
	procSetForegroundWindow.Call(systrayHwnd)
	procAttachThreadInput.Call(ourTID, fgTID, 0) // FALSE = detach
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
			// Force our window to foreground before the library calls TrackPopupMenu.
			// Without this, the menu silently fails when Teams/Chrome has focus.
			forceForeground()

			// Let the original handler call TrackPopupMenu (blocking).
			ret, _, _ := procCallWindowProc.Call(
				originalWndProc, hwnd, uintptr(msg), wParam, lParam,
			)
			// Fix: KB Q135788 - PostMessage WM_NULL ensures menu dismisses properly.
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
