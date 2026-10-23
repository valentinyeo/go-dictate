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
	meetingCacheDuration = 3 * time.Second

	kernel32DLL                    = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32DLL.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32DLL.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = kernel32DLL.NewProc("CloseHandle")
)

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

func checkMicPrivacyRegistry() bool {
	const basePath = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\microphone`

	excludedApps := []string{
		"go-dictate",
		"beeper",
		"immersivecontrolpanel",
		"windowssoundrecorder",
	}

	regPaths := []string{
		basePath + `\NonPackaged`,
		basePath,
	}

	for _, regPath := range regPaths {
		key, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.READ)
		if err != nil {
			continue
		}

		subKeys, err := key.ReadSubKeyNames(-1)
		key.Close()
		if err != nil {
			continue
		}

		for _, subKeyName := range subKeys {
			if strings.EqualFold(subKeyName, "NonPackaged") {
				continue
			}

			subKey, err := registry.OpenKey(registry.CURRENT_USER, regPath+`\`+subKeyName, registry.READ)
			if err != nil {
				continue
			}

			start, _, errStart := subKey.GetIntegerValue("LastUsedTimeStart")
			stop, _, errStop := subKey.GetIntegerValue("LastUsedTimeStop")
			subKey.Close()

			if errStart != nil || start == 0 {
				continue
			}

			if stop == 0 || (errStop != nil && start > 0) {
				nameLower := strings.ToLower(subKeyName)
				excluded := false
				for _, app := range excludedApps {
					if strings.Contains(nameLower, app) {
						excluded = true
						break
					}
				}
				if excluded {
					continue
				}

				appName := registryKeyToAppName(subKeyName)
				fmt.Printf("[MeetingDetector] Mic actively used by: %s\n", appName)
				return true
			}
		}
	}

	return false
}

func registryKeyToAppName(key string) string {
	path := strings.ReplaceAll(key, "#", "\\")
	parts := strings.Split(path, "\\")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return key
}

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
