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

var gwlpWndProc = ^uintptr(3)

var (
	originalWndProc uintptr
	clickManager    *Manager
	systrayHwnd     uintptr

	subclassedWndProc = syscall.NewCallback(trayWndProc)
)

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

func forceForeground() {
	fgHwnd, _, _ := procGetForegroundWindow.Call()
	if fgHwnd == 0 || fgHwnd == systrayHwnd {
		return
	}

	fgTID, _, _ := procGetWindowThreadProcessId.Call(fgHwnd, 0)
	ourTID, _, _ := procGetCurrentThreadId.Call()
	if fgTID == ourTID || fgTID == 0 {
		return
	}

	procAttachThreadInput.Call(ourTID, fgTID, 1)
	procSetForegroundWindow.Call(systrayHwnd)
	procAttachThreadInput.Call(ourTID, fgTID, 0)
}

func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	const wmSystrayMessage = wmUser + 1

	if msg == wmSystrayMessage {
		switch lParam {
		case wmLButtonUp:
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
			forceForeground()

			ret, _, _ := procCallWindowProc.Call(
				originalWndProc, hwnd, uintptr(msg), wParam, lParam,
			)
			procPostMessage.Call(hwnd, wmNull, 0, 0)
			return ret
		}
	}

	ret, _, _ := procCallWindowProc.Call(
		originalWndProc, hwnd, uintptr(msg), wParam, lParam,
	)
	return ret
}
