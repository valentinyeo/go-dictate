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
	cacheDuration = 3 * time.Second // Check at most every 3s (media state changes slowly)
)

var (
	ole32               = syscall.NewLazyDLL("ole32.dll")
	coInitializeEx      = ole32.NewProc("CoInitializeEx")
	coUninitialize      = ole32.NewProc("CoUninitialize")
	coCreateInstance    = ole32.NewProc("CoCreateInstance")

	// MMDeviceEnumerator CLSID: {BCDE0395-E52F-467C-8E3D-C4579291692E}
	CLSID_MMDeviceEnumerator = syscall.GUID{
		Data1: 0xBCDE0395,
		Data2: 0xE52F,
		Data3: 0x467C,
		Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E},
	}

	// IMMDeviceEnumerator IID: {A95664D2-9614-4F35-A746-DE8DB63617E6}
	IID_IMMDeviceEnumerator = syscall.GUID{
		Data1: 0xA95664D2,
		Data2: 0x9614,
		Data3: 0x4F35,
		Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6},
	}

	// IAudioSessionManager2 IID: {77AA99A0-1BD6-484F-8BC7-2C654C9A9B6F}
	IID_IAudioSessionManager2 = syscall.GUID{
		Data1: 0x77AA99A0,
		Data2: 0x1BD6,
		Data3: 0x484F,
		Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F},
	}
)

const (
	CLSCTX_INPROC_SERVER = 0x1
	eRender              = 0 // Audio rendering (speakers)
	eCapture             = 1 // Audio capture (microphone)
	eConsole             = 0 // Console audio device
	AudioSessionStateActive = 1 // Active audio session
)

// IsMediaPlayingCached returns the last cached result without triggering
// a new COM call. Safe to call from hot paths like audio chunk processing.
func IsMediaPlayingCached() bool {
	mediaCache.Lock()
	defer mediaCache.Unlock()
	return mediaCache.isPlaying
}

// IsMediaPlaying detects if any audio is playing through speakers
// Uses a 3s cache to avoid expensive COM calls on every audio chunk
func IsMediaPlaying() bool {
	mediaCache.Lock()
	defer mediaCache.Unlock()

	// Return cached result if still valid
	if time.Since(mediaCache.lastCheck) < cacheDuration {
		return mediaCache.isPlaying
	}

	// Update cache
	mediaCache.isPlaying = checkMediaPlayback()
	mediaCache.lastCheck = time.Now()
	return mediaCache.isPlaying
}

// checkMediaPlayback performs the actual Windows API check
func checkMediaPlayback() bool {
	// Lock this goroutine to the current OS thread for COM safety
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize COM
	ret, _, _ := coInitializeEx.Call(0, 0x0) // COINIT_MULTITHREADED
	if ret != 0 && ret != 0x00000001 { // S_OK or S_FALSE (already initialized)
		return false
	}
	defer coUninitialize.Call()

	// Create MMDeviceEnumerator
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
	defer syscall.Syscall(getVTable(deviceEnumerator, 2), 1, deviceEnumerator, 0, 0) // Release

	// Get default audio endpoint (speakers)
	var device uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(deviceEnumerator, 4), // GetDefaultAudioEndpoint
		4,
		deviceEnumerator,
		uintptr(eRender),  // Render device (speakers)
		uintptr(eConsole), // Console role
		uintptr(unsafe.Pointer(&device)),
		0,
		0,
	)
	if ret != 0 || device == 0 {
		return false
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0) // Release

	// Activate IAudioSessionManager2
	var sessionManager uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(device, 3), // Activate
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
	defer syscall.Syscall(getVTable(sessionManager, 2), 1, sessionManager, 0, 0) // Release

	// Get session enumerator
	var sessionEnumerator uintptr
	ret, _, _ = syscall.Syscall(
		getVTable(sessionManager, 5), // GetSessionEnumerator
		2,
		sessionManager,
		uintptr(unsafe.Pointer(&sessionEnumerator)),
		0,
	)
	if ret != 0 || sessionEnumerator == 0 {
		return false
	}
	defer syscall.Syscall(getVTable(sessionEnumerator, 2), 1, sessionEnumerator, 0, 0) // Release

	// Get session count
	var sessionCount int32
	ret, _, _ = syscall.Syscall(
		getVTable(sessionEnumerator, 3), // GetCount
		2,
		sessionEnumerator,
		uintptr(unsafe.Pointer(&sessionCount)),
		0,
	)
	if ret != 0 {
		return false
	}

	// Get our own process ID to exclude it
	ourPID := getCurrentProcessID()

	// Check each session
	for i := int32(0); i < sessionCount; i++ {
		var session uintptr
		ret, _, _ = syscall.Syscall(
			getVTable(sessionEnumerator, 4), // GetSession
			3,
			sessionEnumerator,
			uintptr(i),
			uintptr(unsafe.Pointer(&session)),
		)
		if ret != 0 || session == 0 {
			continue
		}

		// Get session state
		var state int32
		syscall.Syscall(
			getVTable(session, 3), // GetState
			2,
			session,
			uintptr(unsafe.Pointer(&state)),
			0,
		)

		// Get process ID for this session
		var processID uint32
		syscall.Syscall(
			getVTable(session, 14), // GetProcessId
			2,
			session,
			uintptr(unsafe.Pointer(&processID)),
			0,
		)

		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0) // Release

		// Check if session is active and not our own process
		if state == AudioSessionStateActive && processID != ourPID {
			return true // Found active audio from another process
		}
	}

	return false
}

// getVTable gets a function pointer from COM vtable
func getVTable(obj uintptr, index int) uintptr {
	vtable := *(**uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(vtable)) + uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

// getCurrentProcessID returns the current process ID
func getCurrentProcessID() uint32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getCurrentProcessId := kernel32.NewProc("GetCurrentProcessId")
	pid, _, _ := getCurrentProcessId.Call()
	return uint32(pid)
}
