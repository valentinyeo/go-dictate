package audio

import (
	"fmt"
	"math"
	"runtime"
	"sync/atomic"
	"time"

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
	device           *malgo.Device
	ctx              *malgo.AllocatedContext
	lastCallbackNano atomic.Int64  // UnixNano timestamp of last audio callback
	peakRMS          atomic.Uint64 // Peak RMS (float64 bits) over recent window, reset on read
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

	// Buffer size for ~80ms chunks (1280 samples at 16kHz). Deepgram Flux
	// recommends 80ms chunks for optimal streaming latency, and Nova works
	// fine with the slightly smaller cadence too.
	deviceConfig.PeriodSizeInFrames = 1280

	sizeInBytes := uint32(deviceConfig.PeriodSizeInFrames * 2) // samples * 2 bytes per sample

	// Create capture object first so callback can reference it
	c := &Capture{ctx: ctx}
	c.lastCallbackNano.Store(time.Now().UnixNano())

	onRecvFrames := func(pOutputSample, pInputSamples []byte, framecount uint32) {
		c.lastCallbackNano.Store(time.Now().UnixNano())

		// Convert bytes to int16 samples
		samples := make([]int16, framecount)
		for i := uint32(0); i < framecount; i++ {
			// Little-endian conversion
			samples[i] = int16(pInputSamples[i*2]) | int16(pInputSamples[i*2+1])<<8
		}

		// Track peak RMS for watchdog audio quality monitoring
		var sumSquares float64
		for _, s := range samples {
			normalized := float64(s) / float64(math.MaxInt16)
			sumSquares += normalized * normalized
		}
		rms := math.Sqrt(sumSquares / float64(len(samples)))
		// Atomically update peak: load current, store if new is higher
		for {
			old := c.peakRMS.Load()
			oldRMS := math.Float64frombits(old)
			if rms <= oldRMS {
				break
			}
			if c.peakRMS.CompareAndSwap(old, math.Float64bits(rms)) {
				break
			}
		}

		// Send to channel (non-blocking)
		select {
		case audioChunks <- samples:
		default:
			// Drop frames if channel is full
			fmt.Println("[Audio] Warning: Dropping frames, channel full")
		}
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: onRecvFrames,
	})
	if err != nil {
		ctx.Free()
		return nil, fmt.Errorf("failed to initialize audio device: %w", err)
	}
	c.device = device

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

	return c, nil
}

// IsAlive returns true if the audio device callback has fired within the given timeout.
func (c *Capture) IsAlive(timeout time.Duration) bool {
	last := c.lastCallbackNano.Load()
	if last == 0 {
		return true // not started yet
	}
	return time.Since(time.Unix(0, last)) < timeout
}

// PeakRMS returns the peak RMS value since the last call and resets it to zero.
// Used by the watchdog to detect "alive but silent/noisy" microphones.
func (c *Capture) PeakRMS() float64 {
	bits := c.peakRMS.Swap(0)
	return math.Float64frombits(bits)
}

// QuickRestart does a lightweight Stop+Start on the device without tearing
// down the malgo context. This preserves driver state and is faster than a
// full Stop+NewCapture cycle. Returns an error if the device can't restart.
func (c *Capture) QuickRestart() error {
	if c.device == nil {
		return fmt.Errorf("no device")
	}
	c.device.Stop()
	time.Sleep(200 * time.Millisecond)
	if err := c.device.Start(); err != nil {
		return fmt.Errorf("device start failed: %w", err)
	}
	c.lastCallbackNano.Store(time.Now().UnixNano())
	c.peakRMS.Store(0)
	return nil
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
