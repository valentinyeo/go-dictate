package notify

import (
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	user32           = windows.NewLazySystemDLL("user32.dll")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	pShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	pCreateWindowEx  = user32.NewProc("CreateWindowExW")
	pDestroyWindow   = user32.NewProc("DestroyWindow")
	pRegisterClass   = user32.NewProc("RegisterClassExW")
	pUnregisterClass = user32.NewProc("UnregisterClassW")
	pDefWindowProc   = user32.NewProc("DefWindowProcW")
	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	pLoadIcon        = user32.NewProc("LoadIconW")
)

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002
	nifIcon   = 0x00000002
	nifTip    = 0x00000004
	nifInfo   = 0x00000010
	niifInfo  = 0x00000001
)

type notifyIconData struct {
	Size                       uint32
	Wnd                        windows.Handle
	ID, Flags, CallbackMessage uint32
	Icon                       windows.Handle
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	Timeout, Version           uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	GuidItem                   windows.GUID
	BalloonIcon                windows.Handle
}

type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background windows.Handle
	MenuName, ClassName                *uint16
}

// ShowBalloon displays a Windows balloon notification from the system tray.
// The notification auto-dismisses after the given duration.
func ShowBalloon(title, message string, duration time.Duration) {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := pGetModuleHandle.Call(0)

		// Register a minimal window class
		className, _ := windows.UTF16PtrFromString("GoDictateBalloonClass")
		wndProc := windows.NewCallback(func(hwnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
			ret, _, _ := pDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
			return ret
		})

		wcex := wndClassEx{
			Size:      uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:   wndProc,
			Instance:  windows.Handle(hInst),
			ClassName: className,
		}
		pRegisterClass.Call(uintptr(unsafe.Pointer(&wcex)))

		// Create a message-only window (HWND_MESSAGE parent)
		hwndMessage, _ := windows.UTF16PtrFromString("HWND_MESSAGE")
		_ = hwndMessage
		hwnd, _, _ := pCreateWindowEx.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			0, // no title
			0, // no style
			0, 0, 0, 0,
			uintptr(0xFFFFFFFFFFFFFFFD), // HWND_MESSAGE = (HWND)-3
			0, hInst, 0,
		)
		if hwnd == 0 {
			return
		}

		// Load default app icon
		const idiApplication = 32512
		icon, _, _ := pLoadIcon.Call(0, uintptr(idiApplication))

		// Prepare notification data
		nid := notifyIconData{
			Size:      uint32(unsafe.Sizeof(notifyIconData{})),
			Wnd:       windows.Handle(hwnd),
			ID:        99,
			Flags:     nifIcon | nifTip | nifInfo,
			Icon:      windows.Handle(icon),
			InfoFlags: niifInfo,
		}

		// Set tip
		tip, _ := windows.UTF16FromString("Dictation App")
		copy(nid.Tip[:], tip)

		// Set balloon title and message
		titleU, _ := windows.UTF16FromString(title)
		copy(nid.InfoTitle[:], titleU)
		msgU, _ := windows.UTF16FromString(message)
		copy(nid.Info[:], msgU)

		// Show the balloon
		pShellNotifyIcon.Call(uintptr(nimAdd), uintptr(unsafe.Pointer(&nid)))

		// Wait, then clean up
		time.Sleep(duration)

		pShellNotifyIcon.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&nid)))
		pDestroyWindow.Call(hwnd)
		pUnregisterClass.Call(uintptr(unsafe.Pointer(className)), hInst)
	}()
}
