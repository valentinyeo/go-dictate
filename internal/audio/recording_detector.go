package audio

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	recordingCache struct {
		sync.Mutex
		isRecording bool
		lastCheck   time.Time
	}
	recordingCacheDuration = 2 * time.Second
)

var (
	kernel32Snap   = syscall.NewLazyDLL("kernel32.dll")
	procCreateSnap = kernel32Snap.NewProc("CreateToolhelp32Snapshot")
	procProcess32  = kernel32Snap.NewProc("Process32FirstW")
	procProcess32N = kernel32Snap.NewProc("Process32NextW")
	procCloseSnap  = kernel32Snap.NewProc("CloseHandle")

	user32DLL           = syscall.NewLazyDLL("user32.dll")
	procEnumWindows     = user32DLL.NewProc("EnumWindows")
	procIsWindowVisible = user32DLL.NewProc("IsWindowVisible")
	procGetWindowTID    = user32DLL.NewProc("GetWindowThreadProcessId")
)

const (
	TH32CS_SNAPPROCESS = 0x00000002
	MAX_PATH_SNAP      = 260
)

type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [MAX_PATH_SNAP]uint16
}

// Processes that indicate active screen recording.
var recordingProcesses = []string{
	"ffmpeg.exe", // screencast2-go, OBS output, etc.
}

// Processes where a visible window (not just tray) means recording is active.
var recordingWindowApps = []string{
	"loom.exe",
}

// Shared state for the EnumWindows callback (protected by recordingCache.Mutex)
var enumState struct {
	pids  map[uint32]bool
	found bool
}

// Create the callback ONCE at init (Go has a global limit of ~2000 callbacks)
var enumWindowsCallback = syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return 1 // continue
	}

	var pid uint32
	procGetWindowTID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if enumState.pids[pid] {
		enumState.found = true
		return 0 // stop enumeration
	}
	return 1 // continue
})

// IsRecordingActive detects if a screen recording is in progress.
func IsRecordingActive() bool {
	recordingCache.Lock()
	defer recordingCache.Unlock()

	now := time.Now()
	if now.Sub(recordingCache.lastCheck) < recordingCacheDuration {
		return recordingCache.isRecording
	}

	recordingCache.isRecording = checkRecording()
	recordingCache.lastCheck = now
	return recordingCache.isRecording
}

func checkRecording() bool {
	snap, _, _ := procCreateSnap.Call(TH32CS_SNAPPROCESS, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return false
	}
	defer procCloseSnap.Call(snap)

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))

	windowAppPIDs := make(map[uint32]bool)

	ret, _, _ := procProcess32.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ret != 0 {
		name := strings.ToLower(syscall.UTF16ToString(entry.szExeFile[:]))

		for _, rp := range recordingProcesses {
			if name == rp {
				return true
			}
		}

		for _, wa := range recordingWindowApps {
			if name == wa {
				windowAppPIDs[entry.th32ProcessID] = true
			}
		}

		entry.dwSize = uint32(unsafe.Sizeof(entry))
		ret, _, _ = procProcess32N.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}

	if len(windowAppPIDs) > 0 {
		return hasVisibleWindowFromPIDs(windowAppPIDs)
	}

	return false
}

// hasVisibleWindowFromPIDs checks if any visible top-level window belongs to the given PIDs.
// Uses the pre-allocated enumWindowsCallback (no resource leak).
// Must be called under recordingCache.Mutex.
func hasVisibleWindowFromPIDs(pids map[uint32]bool) bool {
	enumState.pids = pids
	enumState.found = false

	procEnumWindows.Call(enumWindowsCallback, 0)

	result := enumState.found

	// Keep pids alive until after EnumWindows returns
	runtime.KeepAlive(pids)

	enumState.pids = nil
	return result
}
