package audio

import (
	"fmt"

	"github.com/gen2brain/malgo"
)

type Capture struct {
	device *malgo.Device
	ctx    *malgo.AllocatedContext
}

func NewCapture(audioChunks chan<- []int16) (*Capture, error) {
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
	fmt.Println("[Audio] Audio device initialized")

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
		c.ctx.Free()
		c.ctx.Uninit()
	}
}
