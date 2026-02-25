package audio

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/logger"
)

var (
	// IAudioEndpointVolume IID: {5CDF2C82-841E-4546-9722-0CF74078229A}
	IID_IAudioEndpointVolume = syscall.GUID{
		Data1: 0x5CDF2C82,
		Data2: 0x841E,
		Data3: 0x4546,
		Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A},
	}
)

// AudioDucker manages Windows system volume ducking during speech
type AudioDucker struct {
	duckVolume     float32
	originalVolume float32
	isDucked       bool
	mu             sync.Mutex
}

func NewAudioDucker(duckVolume float32) *AudioDucker {
	return &AudioDucker{
		duckVolume: duckVolume,
	}
}

// Duck reduces the system master volume to duckVolume percentage
func (d *AudioDucker) Duck() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	logger.Info("[AudioDucker] Duck() called - configured duckVolume=%.0f%%, isDucked=%v", d.duckVolume*100, d.isDucked)

	// Already ducked, don't duck again
	if d.isDucked {
		logger.Info("[AudioDucker] Already ducked, skipping")
		return nil
	}

	// Lock OS thread for entire COM operation
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	volume, err := d.getEndpointVolume()
	if err != nil {
		return fmt.Errorf("failed to get endpoint volume: %w", err)
	}
	// DON'T defer release - we need it throughout the function

	// Get current volume level
	var currentVolume float32
	ret, _, _ := syscall.Syscall(
		getVTable(volume, 9), // GetMasterVolumeLevelScalar
		2,
		volume,
		uintptr(unsafe.Pointer(&currentVolume)),
		0,
	)
	if ret != 0 {
		syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0) // Release
		return fmt.Errorf("failed to get current volume: HRESULT 0x%X", ret)
	}

	logger.Info("[AudioDucker] Current master volume: %.0f%%", currentVolume*100)

	// Check if device is muted
	var isMuted int32
	syscall.Syscall(
		getVTable(volume, 10), // GetMute
		2,
		volume,
		uintptr(unsafe.Pointer(&isMuted)),
		0,
	)
	logger.Info("[AudioDucker] Device mute state before duck: %v", isMuted != 0)

	// If muted, unmute it before ducking
	if isMuted != 0 {
		logger.Info("[AudioDucker] Device is muted! Unmuting before ducking...")
		var unmute int32 = 0
		syscall.Syscall(
			getVTable(volume, 11), // SetMute
			3,
			volume,
			uintptr(unmute),
			0,
		)
	}

	// Store original volume
	d.originalVolume = currentVolume

	// Set ducked volume using scalar (0.0-1.0)
	targetVolume := d.duckVolume
	logger.Info("[AudioDucker] Calling SetMasterVolumeLevelScalar with %.0f%%", targetVolume*100)
	ret, _, _ = syscall.Syscall(
		getVTable(volume, 7), // SetMasterVolumeLevelScalar
		3,
		volume,
		uintptr(math.Float32bits(targetVolume)),
		0, // Event context (null)
	)
	logger.Info("[AudioDucker] SetMasterVolumeLevelScalar returned HRESULT: 0x%X", ret)

	if ret != 0 {
		syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0) // Release
		return fmt.Errorf("failed to set ducked volume: HRESULT 0x%X", ret)
	}

	// Verify the volume actually changed
	var actualVolume float32
	syscall.Syscall(
		getVTable(volume, 9), // GetMasterVolumeLevelScalar
		2,
		volume,
		uintptr(unsafe.Pointer(&actualVolume)),
		0,
	)
	logger.Info("[AudioDucker] Volume after ducking: %.0f%%", actualVolume*100)

	// Release the volume interface
	syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0)

	d.isDucked = true
	logger.Info("[AudioDucker] ✅ Ducked master volume: %.0f%% → %.0f%% (actual: %.0f%%)",
		currentVolume*100, targetVolume*100, actualVolume*100)
	return nil
}

// Restore returns the system master volume to its original level
func (d *AudioDucker) Restore() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	logger.Info("[AudioDucker] Restore() called - isDucked=%v, originalVolume=%.0f%%", d.isDucked, d.originalVolume*100)

	// Not ducked, nothing to restore
	if !d.isDucked {
		logger.Info("[AudioDucker] Skipping restore (not currently ducked)")
		return nil
	}

	logger.Info("[AudioDucker] Attempting to restore master volume to %.0f%%", d.originalVolume*100)

	// Lock OS thread for entire COM operation
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	volume, err := d.getEndpointVolume()
	if err != nil {
		logger.Error("[AudioDucker] Failed to get endpoint volume for restore: %v", err)
		return fmt.Errorf("failed to get endpoint volume: %w", err)
	}

	// Check if device is muted
	var isMuted int32
	syscall.Syscall(
		getVTable(volume, 10), // GetMute
		2,
		volume,
		uintptr(unsafe.Pointer(&isMuted)),
		0,
	)
	logger.Info("[AudioDucker] Device mute state: %v", isMuted != 0)

	// If muted, unmute it
	if isMuted != 0 {
		logger.Info("[AudioDucker] Device is muted! Unmuting before restore...")
		var unmute int32 = 0
		syscall.Syscall(
			getVTable(volume, 11), // SetMute
			3,
			volume,
			uintptr(unmute),
			0,
		)
	}

	// Read current volume BEFORE restore
	var currentVolume float32
	syscall.Syscall(
		getVTable(volume, 9), // GetMasterVolumeLevelScalar
		2,
		volume,
		uintptr(unsafe.Pointer(&currentVolume)),
		0,
	)
	logger.Info("[AudioDucker] Current volume before restore: %.0f%%", currentVolume*100)

	// Restore original volume
	logger.Info("[AudioDucker] Calling SetMasterVolumeLevelScalar with %.0f%%", d.originalVolume*100)
	ret, _, _ := syscall.Syscall(
		getVTable(volume, 7), // SetMasterVolumeLevelScalar
		3,
		volume,
		uintptr(math.Float32bits(d.originalVolume)),
		0, // Event context (null)
	)

	logger.Info("[AudioDucker] SetMasterVolumeLevelScalar returned HRESULT: 0x%X", ret)

	// Read volume AFTER restore to verify
	var newVolume float32
	syscall.Syscall(
		getVTable(volume, 9), // GetMasterVolumeLevelScalar
		2,
		volume,
		uintptr(unsafe.Pointer(&newVolume)),
		0,
	)
	logger.Info("[AudioDucker] Volume after restore: %.0f%%", newVolume*100)

	// Release the volume interface
	syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0)

	if ret != 0 {
		logger.Error("[AudioDucker] Failed to restore volume: HRESULT 0x%X", ret)
		return fmt.Errorf("failed to restore volume: HRESULT 0x%X", ret)
	}

	logger.Info("[AudioDucker] ✅ Restored master volume: %.0f%% → %.0f%%", currentVolume*100, newVolume*100)
	d.isDucked = false
	d.originalVolume = 0
	return nil
}

// getEndpointVolume gets the IAudioEndpointVolume interface for the default audio endpoint
func (d *AudioDucker) getEndpointVolume() (uintptr, error) {
	// Initialize COM
	ret, _, _ := coInitializeEx.Call(0, 0x0) // COINIT_MULTITHREADED
	if ret != 0 && ret != 0x00000001 { // S_OK or S_FALSE (already initialized)
		return 0, fmt.Errorf("CoInitializeEx failed: HRESULT 0x%X", ret)
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
		return 0, fmt.Errorf("CoCreateInstance failed: HRESULT 0x%X", ret)
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
		return 0, fmt.Errorf("GetDefaultAudioEndpoint failed: HRESULT 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0) // Release

	// Activate IAudioEndpointVolume
	var endpointVolume uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(device, 3), // Activate
		5,
		device,
		uintptr(unsafe.Pointer(&IID_IAudioEndpointVolume)),
		CLSCTX_INPROC_SERVER,
		0,
		uintptr(unsafe.Pointer(&endpointVolume)),
		0,
	)
	if ret != 0 || endpointVolume == 0 {
		return 0, fmt.Errorf("Activate IAudioEndpointVolume failed: HRESULT 0x%X", ret)
	}

	return endpointVolume, nil
}
