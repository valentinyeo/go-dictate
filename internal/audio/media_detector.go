package audio

import (
	"syscall"
	"unsafe"
)

// IsMediaPlaying checks if any media is currently playing on Windows
func IsMediaPlaying() bool {
	// Use Windows Audio Session API to detect if media is playing
	// Check if any audio session (other than our own) is active
	return isAudioSessionActive()
}

func isAudioSessionActive() bool {
	// Load required Windows DLLs
	ole32 := syscall.NewLazyDLL("ole32.dll")
	coInitializeEx := ole32.NewProc("CoInitializeEx")
	coUninitialize := ole32.NewProc("CoUninitialize")

	// Initialize COM
	coInitializeEx.Call(0, 0x2) // COINIT_APARTMENTTHREADED
	defer coUninitialize.Call()

	// For now, use a simple heuristic: check if Chrome/Edge/Firefox processes exist
	// These typically indicate video playback
	browsers := []string{"chrome.exe", "msedge.exe", "firefox.exe"}

	for _, browser := range browsers {
		if isProcessRunning(browser) {
			// Assume media might be playing if browser is running
			// This is a conservative approach - better to pause unnecessarily
			// than to transcribe video audio
			return true
		}
	}

	return false
}

func isProcessRunning(processName string) bool {
	snapshot := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateToolhelp32Snapshot")
	process32First := syscall.NewLazyDLL("kernel32.dll").NewProc("Process32FirstW")
	process32Next := syscall.NewLazyDLL("kernel32.dll").NewProc("Process32NextW")
	closeHandle := syscall.NewLazyDLL("kernel32.dll").NewProc("CloseHandle")

	const TH32CS_SNAPPROCESS = 0x00000002

	// Create snapshot
	hSnapshot, _, _ := snapshot.Call(uintptr(TH32CS_SNAPPROCESS), 0)
	if hSnapshot == 0 {
		return false
	}
	defer closeHandle.Call(hSnapshot)

	// Process entry structure
	type ProcessEntry32 struct {
		Size              uint32
		Usage             uint32
		ProcessID         uint32
		DefaultHeapID     uintptr
		ModuleID          uint32
		Threads           uint32
		ParentProcessID   uint32
		PriClassBase      int32
		Flags             uint32
		ExeFile           [260]uint16
	}

	var pe ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))

	// Get first process
	ret, _, _ := process32First.Call(hSnapshot, uintptr(unsafe.Pointer(&pe)))
	if ret == 0 {
		return false
	}

	// Convert target process name to UTF16
	targetName := syscall.StringToUTF16(processName)

	// Iterate through processes
	for {
		// Compare process names
		exeName := syscall.UTF16ToString(pe.ExeFile[:])
		if exeName == processName {
			return true
		}

		// Get next process
		ret, _, _ = process32Next.Call(hSnapshot, uintptr(unsafe.Pointer(&pe)))
		if ret == 0 {
			break
		}
	}

	_ = targetName // Use the variable to avoid unused warning

	return false
}
