package audio

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	meetingCache struct {
		sync.Mutex
		isInMeeting bool
		lastCheck   time.Time
	}
	meetingCacheDuration = 3 * time.Second // Check every 3 seconds

	// Cached kernel32 procs for getProcessName
	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32DLL.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32DLL.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = kernel32DLL.NewProc("CloseHandle")
)

// IsMeetingActive detects if the user is in an ACTIVE video call/meeting.
// Checks the Windows CapabilityAccessManager registry to see if any OTHER app
// (besides our own exe) is currently using the microphone. This works for all
// meeting apps: Teams, Zoom, Google Meet in any browser, Discord, etc.
// The registry tracks mic usage per-exe with LastUsedTimeStart/LastUsedTimeStop.
// If start > 0 and stop == 0, the app is actively using the mic right now.
func IsMeetingActive() bool {
	meetingCache.Lock()
	defer meetingCache.Unlock()

	now := time.Now()
	if now.Sub(meetingCache.lastCheck) < meetingCacheDuration {
		return meetingCache.isInMeeting
	}

	meetingCache.isInMeeting = checkMicPrivacyRegistry()
	meetingCache.lastCheck = now
	return meetingCache.isInMeeting
}

// checkMicPrivacyRegistry reads the Windows mic privacy registry to detect
// if any other app is actively using the microphone.
// Registry path: HKCU\Software\Microsoft\Windows\CurrentVersion\
//
//	CapabilityAccessManager\ConsentStore\microphone\NonPackaged
//
// Each sub-key is an exe path (with # instead of \). Values:
//
//	LastUsedTimeStart (REG_QWORD) - FILETIME when mic access started
//	LastUsedTimeStop  (REG_QWORD) - FILETIME when mic access stopped (0 = still active)
func checkMicPrivacyRegistry() bool {
	const regPath = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\microphone\NonPackaged`

	key, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.READ)
	if err != nil {
		return false
	}
	defer key.Close()

	// Get our own exe name to exclude it
	ourExeName := strings.ToLower("go-dictate.exe")

	subKeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return false
	}

	for _, subKeyName := range subKeys {
		subKey, err := registry.OpenKey(key, subKeyName, registry.READ)
		if err != nil {
			continue
		}

		start, _, errStart := subKey.GetIntegerValue("LastUsedTimeStart")
		stop, _, errStop := subKey.GetIntegerValue("LastUsedTimeStop")
		subKey.Close()

		if errStart != nil || start == 0 {
			continue
		}

		// Active if start > 0 and stop == 0 (still using mic)
		if stop == 0 || (errStop != nil && start > 0) {
			// Check it's not our own exe
			nameLower := strings.ToLower(subKeyName)
			if strings.Contains(nameLower, ourExeName) {
				continue
			}

			// Extract a readable app name from the registry key
			appName := registryKeyToAppName(subKeyName)
			fmt.Printf("[MeetingDetector] Mic actively used by: %s\n", appName)
			return true
		}
	}

	return false
}

// registryKeyToAppName converts a registry sub-key name like
// "C:#Program Files (x86)#Microsoft#Edge#Application#msedge.exe"
// to a readable name like "msedge.exe"
func registryKeyToAppName(key string) string {
	// Replace # with \ to get a path, then extract filename
	path := strings.ReplaceAll(key, "#", "\\")
	parts := strings.Split(path, "\\")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return key
}

// checkMicInUseByOtherApp checks WASAPI capture sessions.
// Kept as fallback but the registry approach above is more reliable.
func checkMicInUseByOtherApp() bool {
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
		uintptr(eCapture),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&device)),
		0, 0,
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
		syscall.Syscall(getVTable(session, 3), 2, session, uintptr(unsafe.Pointer(&sessionState)), 0)

		var processID uint32
		syscall.Syscall(getVTable(session, 14), 2, session, uintptr(unsafe.Pointer(&processID)), 0)

		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)

		if sessionState == AudioSessionStateActive && processID != ourPID && processID != 0 {
			return true
		}
	}

	return false
}

// getProcessName gets the executable name for a given process ID
func getProcessName(pid uint32) string {
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

	handle, _, _ := procOpenProcess.Call(uintptr(PROCESS_QUERY_LIMITED_INFORMATION), 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer procCloseHandle.Call(handle)

	var buf [syscall.MAX_PATH]uint16
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(handle, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return ""
	}

	fullPath := syscall.UTF16ToString(buf[:size])
	parts := strings.Split(fullPath, "\\")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
