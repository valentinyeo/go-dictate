package audio

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/logger"
)

// AppVolumeDucker ducks volume for specific applications (Chrome/Edge/Firefox)
type AppVolumeDucker struct {
	duckVolume      float32
	originalVolumes map[uint32]float32 // pid -> original volume
	isDucked        bool
	mu              sync.Mutex
}

// Browser/media app process names to duck
var mediaAppProcesses = []string{
	"chrome.exe",
	"msedge.exe",
	"firefox.exe",
	"brave.exe",
	"opera.exe",
	"vlc.exe",
	"screenbox.exe",
	"Screenbox.exe", // Case variations
	"spotify.exe",
}

func NewAppVolumeDucker(duckVolume float32) *AppVolumeDucker {
	return &AppVolumeDucker{
		duckVolume:      duckVolume,
		originalVolumes: make(map[uint32]float32),
	}
}

// IsDucked reports whether media app volumes are currently lowered.
func (d *AppVolumeDucker) IsDucked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isDucked
}

// Duck reduces volume for browser/media applications
func (d *AppVolumeDucker) Duck() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isDucked {
		logger.Info("[AppVolumeDucker] Already ducked, skipping")
		return nil
	}

	logger.Info("[AppVolumeDucker] Ducking media apps to %.0f%%", d.duckVolume*100)

	// Lock OS thread for entire COM operation
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize COM
	ret, _, _ := coInitializeEx.Call(0, 0x0) // COINIT_MULTITHREADED
	if ret != 0 && ret != 0x00000001 {
		return fmt.Errorf("CoInitializeEx failed")
	}
	defer coUninitialize.Call()

	// Get device enumerator
	var deviceEnumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&deviceEnumerator)),
	)
	if ret != 0 {
		return fmt.Errorf("CoCreateInstance failed")
	}
	defer syscall.Syscall(getVTable(deviceEnumerator, 2), 1, deviceEnumerator, 0, 0)

	// Get default audio endpoint
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
	if ret != 0 {
		return fmt.Errorf("GetDefaultAudioEndpoint failed")
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)

	// Get session manager
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
	if ret != 0 {
		return fmt.Errorf("Activate failed")
	}
	defer syscall.Syscall(getVTable(sessionManager, 2), 1, sessionManager, 0, 0)

	// Get session enumerator
	var sessionEnumerator uintptr
	ret, _, _ = syscall.Syscall(
		getVTable(sessionManager, 5),
		2,
		sessionManager,
		uintptr(unsafe.Pointer(&sessionEnumerator)),
		0,
	)
	if ret != 0 {
		return fmt.Errorf("GetSessionEnumerator failed")
	}
	defer syscall.Syscall(getVTable(sessionEnumerator, 2), 1, sessionEnumerator, 0, 0)

	// Get session count
	var sessionCount int32
	syscall.Syscall(
		getVTable(sessionEnumerator, 3),
		2,
		sessionEnumerator,
		uintptr(unsafe.Pointer(&sessionCount)),
		0,
	)

	duckedCount := 0

	// Duck each media app session
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

		// Get process ID
		var processID uint32
		syscall.Syscall(
			getVTable(session, 14),
			2,
			session,
			uintptr(unsafe.Pointer(&processID)),
			0,
		)

		processName := getProcessName(processID)
		processNameLower := strings.ToLower(processName)

		// Check if this is a media app
		isMediaApp := false
		for _, mediaProc := range mediaAppProcesses {
			if strings.Contains(processNameLower, strings.ToLower(mediaProc)) {
				isMediaApp = true
				break
			}
		}

		if !isMediaApp {
			syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)
			continue
		}

		// Get ISimpleAudioVolume interface
		IID_ISimpleAudioVolume := syscall.GUID{
			Data1: 0x87CE5498,
			Data2: 0x68D6,
			Data3: 0x44E5,
			Data4: [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8},
		}

		var simpleVolume uintptr
		ret, _, _ = syscall.Syscall6(
			getVTable(session, 0), // QueryInterface
			3,
			session,
			uintptr(unsafe.Pointer(&IID_ISimpleAudioVolume)),
			uintptr(unsafe.Pointer(&simpleVolume)),
			0, 0, 0,
		)

		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0) // Release session

		if ret != 0 || simpleVolume == 0 {
			continue
		}

		// Get current volume
		var currentVolume float32
		syscall.Syscall(
			getVTable(simpleVolume, 4), // GetMasterVolume
			2,
			simpleVolume,
			uintptr(unsafe.Pointer(&currentVolume)),
			0,
		)

		logger.Info("[AppVolumeDucker] %s (PID %d) current volume: %.0f%%",
			processName, processID, currentVolume*100)

		// Store original volume
		d.originalVolumes[processID] = currentVolume

		// Set ducked volume - pass float bits directly so Go's asmstdcall
		// loads them into the XMM register (COM expects float by value, not pointer)
		targetVolume := d.duckVolume
		ret, _, _ = syscall.Syscall(
			getVTable(simpleVolume, 3), // SetMasterVolume
			3,
			simpleVolume,
			uintptr(math.Float32bits(targetVolume)),
			0,
		)

		// Verify it actually changed
		var actualVolume float32
		syscall.Syscall(
			getVTable(simpleVolume, 4), // GetMasterVolume
			2,
			simpleVolume,
			uintptr(unsafe.Pointer(&actualVolume)),
			0,
		)

		syscall.Syscall(getVTable(simpleVolume, 2), 1, simpleVolume, 0, 0) // Release

		logger.Info("[AppVolumeDucker] Ducked %s (PID %d): %.0f%% → %.0f%% (actual: %.0f%%)",
			processName, processID, currentVolume*100, targetVolume*100, actualVolume*100)
		duckedCount++
	}

	if duckedCount > 0 {
		d.isDucked = true
		logger.Info("[AppVolumeDucker] ✅ Ducked %d media app(s)", duckedCount)
	} else {
		logger.Info("[AppVolumeDucker] No media apps found to duck")
	}

	return nil
}

// Restore restores original volume for browser/media applications
func (d *AppVolumeDucker) Restore() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isDucked {
		logger.Info("[AppVolumeDucker] Not ducked, skipping restore")
		return nil
	}

	logger.Info("[AppVolumeDucker] Restoring media app volumes (stored %d PIDs)", len(d.originalVolumes))

	// Lock OS thread for entire COM operation
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize COM
	ret, _, _ := coInitializeEx.Call(0, 0x0) // COINIT_MULTITHREADED
	if ret != 0 && ret != 0x00000001 {
		return fmt.Errorf("CoInitializeEx failed")
	}
	defer coUninitialize.Call()

	// Get device enumerator
	var deviceEnumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&deviceEnumerator)),
	)
	if ret != 0 {
		return fmt.Errorf("CoCreateInstance failed")
	}
	defer syscall.Syscall(getVTable(deviceEnumerator, 2), 1, deviceEnumerator, 0, 0)

	// Get default audio endpoint
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
	if ret != 0 {
		return fmt.Errorf("GetDefaultAudioEndpoint failed")
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)

	// Get session manager
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
	if ret != 0 {
		return fmt.Errorf("Activate failed")
	}
	defer syscall.Syscall(getVTable(sessionManager, 2), 1, sessionManager, 0, 0)

	// Get session enumerator
	var sessionEnumerator uintptr
	ret, _, _ = syscall.Syscall(
		getVTable(sessionManager, 5),
		2,
		sessionManager,
		uintptr(unsafe.Pointer(&sessionEnumerator)),
		0,
	)
	if ret != 0 {
		return fmt.Errorf("GetSessionEnumerator failed")
	}
	defer syscall.Syscall(getVTable(sessionEnumerator, 2), 1, sessionEnumerator, 0, 0)

	// Get session count
	var sessionCount int32
	syscall.Syscall(
		getVTable(sessionEnumerator, 3),
		2,
		sessionEnumerator,
		uintptr(unsafe.Pointer(&sessionCount)),
		0,
	)

	restoredCount := 0

	// Restore each media app session that we have stored
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

		// Get process ID
		var processID uint32
		syscall.Syscall(
			getVTable(session, 14),
			2,
			session,
			uintptr(unsafe.Pointer(&processID)),
			0,
		)

		// Check if we have an original volume for this PID
		originalVolume, exists := d.originalVolumes[processID]
		if !exists {
			syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)
			continue
		}

		// Get ISimpleAudioVolume interface
		IID_ISimpleAudioVolume := syscall.GUID{
			Data1: 0x87CE5498,
			Data2: 0x68D6,
			Data3: 0x44E5,
			Data4: [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8},
		}

		var simpleVolume uintptr
		ret, _, _ = syscall.Syscall6(
			getVTable(session, 0), // QueryInterface
			3,
			session,
			uintptr(unsafe.Pointer(&IID_ISimpleAudioVolume)),
			uintptr(unsafe.Pointer(&simpleVolume)),
			0, 0, 0,
		)

		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0) // Release session

		if ret != 0 || simpleVolume == 0 {
			continue
		}

		// Get current volume before restore
		var currentVolume float32
		syscall.Syscall(
			getVTable(simpleVolume, 4), // GetMasterVolume
			2,
			simpleVolume,
			uintptr(unsafe.Pointer(&currentVolume)),
			0,
		)

		// Restore original volume - pass float bits directly (COM expects float by value)
		ret, _, _ = syscall.Syscall(
			getVTable(simpleVolume, 3), // SetMasterVolume
			3,
			simpleVolume,
			uintptr(math.Float32bits(originalVolume)),
			0,
		)

		// Verify it actually changed
		var actualVolume float32
		syscall.Syscall(
			getVTable(simpleVolume, 4), // GetMasterVolume
			2,
			simpleVolume,
			uintptr(unsafe.Pointer(&actualVolume)),
			0,
		)

		syscall.Syscall(getVTable(simpleVolume, 2), 1, simpleVolume, 0, 0) // Release

		processName := getProcessName(processID)
		logger.Info("[AppVolumeDucker] Restored %s (PID %d): %.0f%% → %.0f%% (actual: %.0f%%)",
			processName, processID, currentVolume*100, originalVolume*100, actualVolume*100)
		restoredCount++
	}

	d.isDucked = false
	d.originalVolumes = make(map[uint32]float32) // Clear stored volumes

	logger.Info("[AppVolumeDucker] ✅ Restored %d media app(s)", restoredCount)
	return nil
}
