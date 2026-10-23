package audio

import (
	"fmt"
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

type CaptureDevice struct {
	ID   malgo.DeviceID
	Name string
}

func ListCaptureDevices() ([]CaptureDevice, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, _ := coInitializeEx.Call(0, 0x0)
	if ret != 0 && ret != 0x00000001 {
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
	lastCallbackNano atomic.Int64
	peakRMS          atomic.Uint64
}

func NewCapture(audioChunks chan<- []int16, selectedDevice *CaptureDevice) (*Capture, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audio context: %w", err)
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = 16000
	deviceConfig.Alsa.NoMMap = 1

	if selectedDevice != nil {
		id := selectedDevice.ID
		deviceConfig.Capture.DeviceID = id.Pointer()
	}

	deviceConfig.PeriodSizeInFrames = 1280

	sizeInBytes := uint32(deviceConfig.PeriodSizeInFrames * 2)

	c := &Capture{ctx: ctx}
	c.lastCallbackNano.Store(time.Now().UnixNano())

	onRecvFrames := func(pOutputSample, pInputSamples []byte, framecount uint32) {
		c.lastCallbackNano.Store(time.Now().UnixNano())

		samples := make([]int16, framecount)
		for i := uint32(0); i < framecount; i++ {
			samples[i] = int16(pInputSamples[i*2]) | int16(pInputSamples[i*2+1])<<8
		}

		var sumSquares float64
		for _, s := range samples {
			normalized := float64(s) / float64(math.MaxInt16)
			sumSquares += normalized * normalized
		}
		rms := math.Sqrt(sumSquares / float64(len(samples)))
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

		select {
		case audioChunks <- samples:
		default:
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

	if selectedDevice != nil {
		fmt.Printf("[Audio] Audio device initialized: %s\n", selectedDevice.Name)
	} else {
		fmt.Println("[Audio] Audio device initialized (system default)")
	}

	fmt.Printf("[Audio] Config: 16kHz, mono, i16, %d bytes per period\n", sizeInBytes)

	if err := device.Start(); err != nil {
		device.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("failed to start audio device: %w", err)
	}

	return c, nil
}

func (c *Capture) IsAlive(timeout time.Duration) bool {
	last := c.lastCallbackNano.Load()
	if last == 0 {
		return true
	}
	return time.Since(time.Unix(0, last)) < timeout
}

func (c *Capture) PeakRMS() float64 {
	bits := c.peakRMS.Swap(0)
	return math.Float64frombits(bits)
}

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
	if c == nil {
		return
	}
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
	if c.ctx != nil {
		_ = c.ctx.Uninit()
		c.ctx.Free()
		c.ctx = nil
	}
}
