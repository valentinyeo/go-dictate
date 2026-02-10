package audio

import (
	"fmt"
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
	kernel32Snap    = syscall.NewLazyDLL("kernel32.dll")
	procCreateSnap  = kernel32Snap.NewProc("CreateToolhelp32Snapshot")
	procProcess32   = kernel32Snap.NewProc("Process32FirstW")
	procProcess32N  = kernel32Snap.NewProc("Process32NextW")
	procCloseSnap   = kernel32Snap.NewProc("CloseHandle")

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

// IsRecordingActive detects if a screen recording is in progress.
// Checks for recording indicator processes (ffmpeg) and visible windows
// from recording apps that normally hide to the system tray (Loom).
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
	// Build a snapshot of all processes
	snap, _, _ := procCreateSnap.Call(TH32CS_SNAPPROCESS, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return false
	}
	defer procCloseSnap.Call(snap)

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))

	// Map of process names -> PIDs (for window checks later)
	windowAppPIDs := make(map[uint32]bool)

	ret, _, _ := procProcess32.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ret != 0 {
		name := strings.ToLower(syscall.UTF16ToString(entry.szExeFile[:]))

		// Check if this is a recording indicator process
		for _, rp := range recordingProcesses {
			if name == rp {
				fmt.Printf("[RecordingDetector] Recording detected: %s (PID %d)\n", name, entry.th32ProcessID)
				return true
			}
		}

		// Collect PIDs for window-based apps
		for _, wa := range recordingWindowApps {
			if name == wa {
				windowAppPIDs[entry.th32ProcessID] = true
			}
		}

		entry.dwSize = uint32(unsafe.Sizeof(entry))
		ret, _, _ = procProcess32N.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}

	// Check if any window-based recording apps have visible windows
	if len(windowAppPIDs) > 0 {
		if hasVisibleWindowFromPIDs(windowAppPIDs) {
			return true
		}
	}

	return false
}

// hasVisibleWindowFromPIDs checks if any visible top-level window belongs to the given PIDs.
func hasVisibleWindowFromPIDs(pids map[uint32]bool) bool {
	type callbackData struct {
		pids  map[uint32]bool
		found bool
	}
	data := &callbackData{pids: pids}

	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		d := (*callbackData)(unsafe.Pointer(lParam))

		// Check if window is visible
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1 // continue
		}

		// Get the process ID owning this window
		var pid uint32
		procGetWindowTID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if d.pids[pid] {
			fmt.Printf("[RecordingDetector] Loom recording detected (visible window, PID %d)\n", pid)
			d.found = true
			return 0 // stop enumeration
		}
		return 1 // continue
	})

	procEnumWindows.Call(cb, uintptr(unsafe.Pointer(data)))
	return data.found
}
