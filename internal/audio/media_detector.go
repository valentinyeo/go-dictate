package audio

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	mediaCache struct {
		sync.Mutex
		isPlaying bool
		lastCheck time.Time
	}
	cacheDuration = 3 * time.Second
)

var (
	ole32            = syscall.NewLazyDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coUninitialize   = ole32.NewProc("CoUninitialize")
	coCreateInstance = ole32.NewProc("CoCreateInstance")

	CLSID_MMDeviceEnumerator = syscall.GUID{
		Data1: 0xBCDE0395,
		Data2: 0xE52F,
		Data3: 0x467C,
		Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E},
	}

	IID_IMMDeviceEnumerator = syscall.GUID{
		Data1: 0xA95664D2,
		Data2: 0x9614,
		Data3: 0x4F35,
		Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6},
	}

	IID_IAudioSessionManager2 = syscall.GUID{
		Data1: 0x77AA99A0,
		Data2: 0x1BD6,
		Data3: 0x484F,
		Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F},
	}

	IID_IAudioMeterInformation = syscall.GUID{
		Data1: 0xC02216F6,
		Data2: 0x8C67,
		Data3: 0x4B5B,
		Data4: [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64},
	}

	sysPeakCache struct {
		sync.Mutex
		peak      float32
		lastCheck time.Time
	}
)

const (
	CLSCTX_INPROC_SERVER    = 0x1
	eRender                 = 0
	eCapture                = 1
	eConsole                = 0
	AudioSessionStateActive = 1
)

func IsMediaPlayingCached() bool {
	mediaCache.Lock()
	defer mediaCache.Unlock()
	return mediaCache.isPlaying
}

func IsMediaPlaying() bool {
	mediaCache.Lock()
	defer mediaCache.Unlock()

	if time.Since(mediaCache.lastCheck) < cacheDuration {
		return mediaCache.isPlaying
	}

	mediaCache.isPlaying = checkMediaPlayback()
	mediaCache.lastCheck = time.Now()
	return mediaCache.isPlaying
}

func checkMediaPlayback() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, _ := coInitializeEx.Call(0, 0x0)
	if ret != 0 && ret != 0x00000001 {
		return false
	}
	defer coUninitialize.Call()

	var deviceEnumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&deviceEnumerator)),
	)
	if ret != 0 || deviceEnumerator == 0 {
		return false
	}
	defer syscall.Syscall(getVTable(deviceEnumerator, 2), 1, deviceEnumerator, 0, 0)

	var device uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(deviceEnumerator, 4),
		4,
		deviceEnumerator,
		uintptr(eRender),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&device)),
		0,
		0,
	)
	if ret != 0 || device == 0 {
		return false
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)

	var sessionManager uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(device, 3),
		5,
		device,
		uintptr(unsafe.Pointer(&IID_IAudioSessionManager2)),
		CLSCTX_INPROC_SERVER,
		0,
		uintptr(unsafe.Pointer(&sessionManager)),
		0,
	)
	if ret != 0 || sessionManager == 0 {
		return false
	}
	defer syscall.Syscall(getVTable(sessionManager, 2), 1, sessionManager, 0, 0)

	var sessionEnumerator uintptr
	ret, _, _ = syscall.Syscall(
		getVTable(sessionManager, 5),
		2,
		sessionManager,
		uintptr(unsafe.Pointer(&sessionEnumerator)),
		0,
	)
	if ret != 0 || sessionEnumerator == 0 {
		return false
	}
	defer syscall.Syscall(getVTable(sessionEnumerator, 2), 1, sessionEnumerator, 0, 0)

	var sessionCount int32
	ret, _, _ = syscall.Syscall(
		getVTable(sessionEnumerator, 3),
		2,
		sessionEnumerator,
		uintptr(unsafe.Pointer(&sessionCount)),
		0,
	)
	if ret != 0 {
		return false
	}

	ourPID := getCurrentProcessID()

	for i := int32(0); i < sessionCount; i++ {
		var session uintptr
		ret, _, _ = syscall.Syscall(
			getVTable(sessionEnumerator, 4),
			3,
			sessionEnumerator,
			uintptr(i),
			uintptr(unsafe.Pointer(&session)),
		)
		if ret != 0 || session == 0 {
			continue
		}

		var sessionState int32
		syscall.Syscall(
			getVTable(session, 3),
			2,
			session,
			uintptr(unsafe.Pointer(&sessionState)),
			0,
		)

		var processID uint32
		syscall.Syscall(
			getVTable(session, 14),
			2,
			session,
			uintptr(unsafe.Pointer(&processID)),
			0,
		)

		if sessionState == AudioSessionStateActive && processID != ourPID {
			var meter uintptr
			ret, _, _ = syscall.Syscall6(
				getVTable(session, 0),
				3,
				session,
				uintptr(unsafe.Pointer(&IID_IAudioMeterInformation)),
				uintptr(unsafe.Pointer(&meter)),
				0, 0, 0,
			)
			if ret == 0 && meter != 0 {
				var peakValue float32
				syscall.Syscall(
					getVTable(meter, 3),
					2,
					meter,
					uintptr(unsafe.Pointer(&peakValue)),
					0,
				)
				syscall.Syscall(getVTable(meter, 2), 1, meter, 0, 0)

				if peakValue > 0.0001 {
					syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)
					return true
				}
			}
		}

		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)
	}

	return false
}

func SystemOutputPeak() float32 {
	sysPeakCache.Lock()
	defer sysPeakCache.Unlock()

	if time.Since(sysPeakCache.lastCheck) < 200*time.Millisecond {
		return sysPeakCache.peak
	}
	sysPeakCache.peak = checkSystemOutputPeak()
	sysPeakCache.lastCheck = time.Now()
	return sysPeakCache.peak
}

func checkSystemOutputPeak() float32 {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, _ := coInitializeEx.Call(0, 0x0)
	if ret != 0 && ret != 0x00000001 {
		return 0
	}
	defer coUninitialize.Call()

	var deviceEnumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&deviceEnumerator)),
	)
	if ret != 0 || deviceEnumerator == 0 {
		return 0
	}
	defer syscall.Syscall(getVTable(deviceEnumerator, 2), 1, deviceEnumerator, 0, 0)

	var device uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(deviceEnumerator, 4),
		4,
		deviceEnumerator,
		uintptr(eRender),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&device)),
		0, 0,
	)
	if ret != 0 || device == 0 {
		return 0
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)

	var meter uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(device, 0),
		3,
		device,
		uintptr(unsafe.Pointer(&IID_IAudioMeterInformation)),
		uintptr(unsafe.Pointer(&meter)),
		0, 0, 0,
	)
	if ret != 0 || meter == 0 {
		return 0
	}
	defer syscall.Syscall(getVTable(meter, 2), 1, meter, 0, 0)

	var peak float32
	syscall.Syscall(
		getVTable(meter, 3),
		2,
		meter,
		uintptr(unsafe.Pointer(&peak)),
		0,
	)
	return peak
}

func getVTable(obj uintptr, index int) uintptr {
	vtable := *(**uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(vtable)) + uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

func getCurrentProcessID() uint32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getCurrentProcessId := kernel32.NewProc("GetCurrentProcessId")
	pid, _, _ := getCurrentProcessId.Call()
	return uint32(pid)
}
