package audio

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/logger"
)

type PowerMonitor struct {
	Wake chan struct{}
	stop chan struct{}
	once sync.Once
}

var (
	user32                               = syscall.NewLazyDLL("user32.dll")
	procRegisterClassExW                 = user32.NewProc("RegisterClassExW")
	procCreateWindowExW                  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                   = user32.NewProc("DefWindowProcW")
	procGetMessageW                      = user32.NewProc("GetMessageW")
	procTranslateMessage                 = user32.NewProc("TranslateMessage")
	procDispatchMessageW                 = user32.NewProc("DispatchMessageW")
	procRegisterPowerSettingNotification = user32.NewProc("RegisterPowerSettingNotification")
	procDestroyWindow                    = user32.NewProc("DestroyWindow")
)

var guidConsoleDisplayState = [16]byte{
	0x56, 0x95, 0xE6, 0x6F, 0x4A, 0x70, 0xA0, 0x47,
	0x8F, 0x24, 0xC2, 0x8D, 0x93, 0x6F, 0xDA, 0x47,
}

const (
	wmPowerBroadcast      = 0x0218
	pbtAPMResumeAutomatic = 0x0012
	pbtPowerSettingChange = 0x8013
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  uintptr
	lpszClassName uintptr
	hIconSm       uintptr
}

type msg struct {
	hWnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
}

type powerBroadcastSetting struct {
	PowerSetting [16]byte
	DataLength   uint32
	Data         [1]byte
}

var powerMonitorInstance *PowerMonitor

func NewPowerMonitor() *PowerMonitor {
	pm := &PowerMonitor{
		Wake: make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
	powerMonitorInstance = pm
	go pm.run()
	return pm
}

func (pm *PowerMonitor) Stop() {
	pm.once.Do(func() {
		close(pm.stop)
	})
}

var powerWndProc = syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == wmPowerBroadcast {
		if wParam == pbtAPMResumeAutomatic {
			logger.Info("[Power] System resumed from sleep (PBT_APMRESUMEAUTOMATIC)")
			if powerMonitorInstance != nil {
				select {
				case powerMonitorInstance.Wake <- struct{}{}:
				default:
				}
			}
		} else if wParam == pbtPowerSettingChange && lParam != 0 {
			setting := (*powerBroadcastSetting)(unsafe.Pointer(lParam))
			if setting.PowerSetting == guidConsoleDisplayState && setting.DataLength >= 1 {
				displayState := setting.Data[0]
				if displayState == 1 {
					logger.Info("[Power] Display turned ON (wake detected)")
					if powerMonitorInstance != nil {
						select {
						case powerMonitorInstance.Wake <- struct{}{}:
						default:
						}
					}
				}
			}
		}
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
})

func (pm *PowerMonitor) run() {
	className, _ := syscall.UTF16PtrFromString("GoDictatePowerWnd")

	wc := wndClassExW{
		lpfnWndProc:   powerWndProc,
		lpszClassName: uintptr(unsafe.Pointer(className)),
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))

	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		0,
		0, 0, 0, 0,
		0,
		0, 0, 0,
	)
	if hwnd == 0 {
		logger.Error("[Power] Failed to create message window")
		return
	}

	procRegisterPowerSettingNotification.Call(
		hwnd,
		uintptr(unsafe.Pointer(&guidConsoleDisplayState)),
		0,
	)

	logger.Info("[Power] Power monitor started, listening for sleep/wake events")

	var m msg
	for {
		select {
		case <-pm.stop:
			procDestroyWindow.Call(hwnd)
			return
		default:
		}

		ret, _, _ := procGetMessageW.Call(
			uintptr(unsafe.Pointer(&m)),
			hwnd,
			0, 0,
		)
		if ret == 0 || ret == uintptr(^uintptr(0)) {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
