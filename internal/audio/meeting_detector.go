package audio

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	meetingCache struct {
		sync.Mutex
		isInMeeting bool
		lastCheck   time.Time
	}
	meetingCacheDuration = 3 * time.Second // Check every 3 seconds

	// Cached kernel32 procs for getProcessName (avoid creating on every call)
	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32DLL.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32DLL.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = kernel32DLL.NewProc("CloseHandle")
)

// IsMeetingActive detects if the user is in an ACTIVE video call/meeting.
// Works by checking if any OTHER app has an active audio session on the
// capture (microphone) device. If another app is using the mic, the user
// is almost certainly in a call. This detects ALL meeting apps (Teams, Zoom,
// Google Meet in browser, Discord, etc.) without needing a process name list.
func IsMeetingActive() bool {
	meetingCache.Lock()
	defer meetingCache.Unlock()

	// Use cache to avoid expensive COM checks on every audio chunk
	now := time.Now()
	if now.Sub(meetingCache.lastCheck) < meetingCacheDuration {
		return meetingCache.isInMeeting
	}

	meetingCache.isInMeeting = checkMicInUseByOtherApp()
	meetingCache.lastCheck = now
	return meetingCache.isInMeeting
}

// checkMicInUseByOtherApp checks if any app (other than ourselves) has an
// active audio session on the default capture device (microphone).
func checkMicInUseByOtherApp() bool {
	// Lock this goroutine to the current OS thread for COM safety
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize COM
	ret, _, _ := coInitializeEx.Call(0, 0x0) // COINIT_MULTITHREADED
	if ret != 0 && ret != 0x00000001 {       // S_OK or S_FALSE (already initialized)
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

	// Get default CAPTURE device (microphone) — not render (speakers)
	var device uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(deviceEnumerator, 4), // GetDefaultAudioEndpoint
		4,
		deviceEnumerator,
		uintptr(eCapture), // Capture device (microphone)
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

	// Check each audio session on the capture device
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
		var sessionState int32
		syscall.Syscall(
			getVTable(session, 3), // GetState
			2,
			session,
			uintptr(unsafe.Pointer(&sessionState)),
			0,
		)

		// Get process ID for this audio session
		var processID uint32
		syscall.Syscall(
			getVTable(session, 14), // GetProcessId
			2,
			session,
			uintptr(unsafe.Pointer(&processID)),
			0,
		)

		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0) // Release

		// If any OTHER process has an active capture session, it's using the mic
		if sessionState == AudioSessionStateActive && processID != ourPID && processID != 0 {
			processName := getProcessName(processID)
			fmt.Printf("[MeetingDetector] Mic in use by: %s (PID: %d)\n", processName, processID)
			return true
		}
	}

	return false
}

// getProcessName gets the executable name for a given process ID
func getProcessName(pid uint32) string {
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

	// Open process (using cached kernel32 procs)
	handle, _, _ := procOpenProcess.Call(uintptr(PROCESS_QUERY_LIMITED_INFORMATION), 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer procCloseHandle.Call(handle)

	// Get process image name
	var buf [syscall.MAX_PATH]uint16
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(handle, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return ""
	}

	fullPath := syscall.UTF16ToString(buf[:size])

	// Extract just the filename from the full path
	parts := strings.Split(fullPath, "\\")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return ""
}
