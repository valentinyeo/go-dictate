package audio

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
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

	// Already ducked, don't duck again
	if d.isDucked {
		return nil
	}

	volume, err := d.getEndpointVolume()
	if err != nil {
		return fmt.Errorf("failed to get endpoint volume: %w", err)
	}
	defer syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0) // Release

	// Get current volume level
	var currentVolume float32
	ret, _, _ := syscall.Syscall(
		getVTable(volume, 7), // GetMasterVolumeLevelScalar
		2,
		volume,
		uintptr(unsafe.Pointer(&currentVolume)),
		0,
	)
	if ret != 0 {
		return fmt.Errorf("failed to get current volume: HRESULT 0x%X", ret)
	}

	// Store original volume
	d.originalVolume = currentVolume

	// Set ducked volume
	ret, _, _ = syscall.Syscall(
		getVTable(volume, 8), // SetMasterVolumeLevelScalar
		3,
		volume,
		uintptr(unsafe.Pointer(&d.duckVolume)),
		0, // Event context (null)
	)
	if ret != 0 {
		return fmt.Errorf("failed to set ducked volume: HRESULT 0x%X", ret)
	}

	d.isDucked = true
	fmt.Printf("[AudioDucker] Ducked volume: %.0f%% → %.0f%%\n", currentVolume*100, d.duckVolume*100)
	return nil
}

// Restore returns the system master volume to its original level
func (d *AudioDucker) Restore() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Not ducked, nothing to restore
	if !d.isDucked {
		return nil
	}

	volume, err := d.getEndpointVolume()
	if err != nil {
		return fmt.Errorf("failed to get endpoint volume: %w", err)
	}
	defer syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0) // Release

	// Restore original volume
	ret, _, _ := syscall.Syscall(
		getVTable(volume, 8), // SetMasterVolumeLevelScalar
		3,
		volume,
		uintptr(unsafe.Pointer(&d.originalVolume)),
		0, // Event context (null)
	)
	if ret != 0 {
		return fmt.Errorf("failed to restore volume: HRESULT 0x%X", ret)
	}

	fmt.Printf("[AudioDucker] Restored volume: %.0f%% → %.0f%%\n", d.duckVolume*100, d.originalVolume*100)
	d.isDucked = false
	d.originalVolume = 0
	return nil
}

// getEndpointVolume gets the IAudioEndpointVolume interface for the default audio endpoint
func (d *AudioDucker) getEndpointVolume() (uintptr, error) {
	// Initialize COM
	ret, _, _ := coInitializeEx.Call(0, 0x2) // COINIT_APARTMENTTHREADED
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
