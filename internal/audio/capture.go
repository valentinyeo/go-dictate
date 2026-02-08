package audio

import (
	"fmt"
	"runtime"

	"github.com/gen2brain/malgo"
)

// CaptureDevice represents an available audio capture device.
type CaptureDevice struct {
	ID   malgo.DeviceID
	Name string
}

// ListCaptureDevices enumerates all available audio capture devices.
func ListCaptureDevices() ([]CaptureDevice, error) {
	// WASAPI device enumeration requires COM on the calling OS thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, _ := coInitializeEx.Call(0, 0x0) // COINIT_MULTITHREADED
	if ret != 0 && ret != 0x00000001 {       // S_OK or S_FALSE
		return nil, fmt.Errorf("CoInitializeEx failed: 0x%X", ret)
	}
	defer coUninitialize.Call()

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audio context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate capture devices: %w", err)
	}

	var devices []CaptureDevice
	for _, info := range infos {
		devices = append(devices, CaptureDevice{
			ID:   info.ID,
			Name: info.Name(),
		})
	}
	return devices, nil
}

type Capture struct {
	device *malgo.Device
	ctx    *malgo.AllocatedContext
}

// NewCapture creates a new audio capture. Pass a *CaptureDevice to select a
// specific microphone, or nil to use the system default.
func NewCapture(audioChunks chan<- []int16, selectedDevice *CaptureDevice) (*Capture, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audio context: %w", err)
	}

	// Configure device for 16kHz mono i16
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = 16000
	deviceConfig.Alsa.NoMMap = 1

	// Select specific device if requested
	if selectedDevice != nil {
		id := selectedDevice.ID
		deviceConfig.Capture.DeviceID = id.Pointer()
	}

	// Buffer size for ~100ms chunks (1600 samples at 16kHz)
	deviceConfig.PeriodSizeInFrames = 1600

	var device *malgo.Device
	sizeInBytes := uint32(1600 * 2) // 1600 samples * 2 bytes per sample

	onRecvFrames := func(pOutputSample, pInputSamples []byte, framecount uint32) {
		// Convert bytes to int16 samples
		samples := make([]int16, framecount)
		for i := uint32(0); i < framecount; i++ {
			// Little-endian conversion
			samples[i] = int16(pInputSamples[i*2]) | int16(pInputSamples[i*2+1])<<8
		}

		// Send to channel (non-blocking)
		select {
		case audioChunks <- samples:
		default:
			// Drop frames if channel is full
			fmt.Println("[Audio] Warning: Dropping frames, channel full")
		}
	}

	device, err = malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: onRecvFrames,
	})
	if err != nil {
		ctx.Free()
		return nil, fmt.Errorf("failed to initialize audio device: %w", err)
	}

	// Device info
	if selectedDevice != nil {
		fmt.Printf("[Audio] Audio device initialized: %s\n", selectedDevice.Name)
	} else {
		fmt.Println("[Audio] Audio device initialized (system default)")
	}

	fmt.Printf("[Audio] Config: 16kHz, mono, i16, %d bytes per period\n", sizeInBytes)

	// Start the device
	if err := device.Start(); err != nil {
		device.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("failed to start audio device: %w", err)
	}

	return &Capture{
		device: device,
		ctx:    ctx,
	}, nil
}

func (c *Capture) Stop() {
	if c.device != nil {
		c.device.Uninit()
	}
	if c.ctx != nil {
		_ = c.ctx.Uninit() // must uninit before free
		c.ctx.Free()
	}
}
