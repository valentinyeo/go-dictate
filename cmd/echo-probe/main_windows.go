package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

type gap struct {
	FromUnixNano int64   `json:"from_unix_nano"`
	ToUnixNano   int64   `json:"to_unix_nano"`
	Milliseconds float64 `json:"milliseconds"`
}

type stream struct {
	DeviceName            string `json:"device_name"`
	DeviceID              string `json:"device_id"`
	StartUnixNano         int64  `json:"start_unix_nano"`
	StopUnixNano          int64  `json:"stop_unix_nano"`
	Frames                uint64 `json:"frames"`
	CallbackCount         uint64 `json:"callback_count"`
	FirstCallbackUnixNano int64  `json:"first_callback_unix_nano"`
	FirstSampleUnixNano   int64  `json:"estimated_first_sample_unix_nano"`
	Gaps                  []gap  `json:"gaps"`
	Error                 string `json:"error,omitempty"`
	mu                    sync.Mutex
	samples               []int16
	lastFrames            time.Time
}

type manifest struct {
	StartTime               time.Time `json:"start_time"`
	EndTime                 time.Time `json:"end_time"`
	RequestedSeconds        int       `json:"requested_seconds"`
	Seconds                 float64   `json:"seconds"`
	SampleRate              int       `json:"sample_rate"`
	Channels                int       `json:"channels"`
	BitsPerSample           int       `json:"bits_per_sample"`
	Alignment               string    `json:"alignment"`
	FirstCallbackOffsetNano *int64    `json:"mic_minus_render_first_callback_offset_nano"`
	Mic                     *stream   `json:"mic"`
	Render                  *stream   `json:"render"`
}

func (s *stream) addGap(now time.Time) {
	if now.Sub(s.lastFrames) > 100*time.Millisecond {
		s.Gaps = append(s.Gaps, gap{s.lastFrames.UnixNano(), now.UnixNano(), float64(now.Sub(s.lastFrames)) / float64(time.Millisecond)})
	}
}

func (s *stream) receive(_ []byte, input []byte, frames uint32) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CallbackCount++
	if s.FirstCallbackUnixNano == 0 {
		s.FirstCallbackUnixNano = now.UnixNano()
	}
	if frames == 0 {
		return
	}
	if uint64(len(input)) < uint64(frames)*2 {
		s.Error = "callback input shorter than the PCM16 frame count"
		return
	}
	s.addGap(now)
	if s.FirstSampleUnixNano == 0 {
		s.FirstSampleUnixNano = now.Add(-time.Duration(frames) * time.Second / sampleRate).UnixNano()
	}
	s.lastFrames = now
	for i := 0; i < int(frames); i++ {
		s.samples = append(s.samples, int16(binary.LittleEndian.Uint16(input[i*2:])))
	}
	s.Frames += uint64(frames)
}

func (s *stream) start(device *malgo.Device) error {
	s.mu.Lock()
	s.lastFrames = time.Now()
	s.StartUnixNano = s.lastFrames.UnixNano()
	s.mu.Unlock()
	return device.Start()
}

func (s *stream) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.StopUnixNano = now.UnixNano()
	s.addGap(now)
}

func initStream(ctx malgo.Context, kind malgo.DeviceType, info malgo.DeviceInfo) (*stream, *malgo.Device, error) {
	s := &stream{DeviceName: info.Name(), DeviceID: info.ID.String(), Gaps: make([]gap, 0)}
	config := malgo.DefaultDeviceConfig(kind)
	config.Capture.DeviceID = unsafe.Pointer(&info.ID)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = 1
	config.Capture.ShareMode = malgo.Shared
	config.SampleRate = sampleRate
	config.PeriodSizeInFrames = envelopeFrames
	config.Wasapi.NoAutoStreamRouting = 1
	device, err := malgo.InitDevice(ctx, config, malgo.DeviceCallbacks{Data: s.receive})
	runtime.KeepAlive(info)
	if err != nil {
		return nil, nil, err
	}
	if device.SampleRate() != sampleRate || device.CaptureChannels() != 1 || device.CaptureFormat() != malgo.FormatS16 {
		device.Uninit()
		return nil, nil, fmt.Errorf("unexpected capture format for %s", info.Name())
	}
	return s, device, nil
}

func writeWAV(path string, samples []int16) (err error) {
	dataBytes := uint64(len(samples)) * 2
	if dataBytes > uint64(^uint32(0))-36 {
		return fmt.Errorf("%s exceeds the RIFF size limit", path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(dataBytes)+36)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], sampleRate)
	binary.LittleEndian.PutUint32(header[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(dataBytes))
	if _, err = file.Write(header); err != nil {
		return err
	}
	return binary.Write(file, binary.LittleEndian, samples)
}

func writeManifest(path string, m manifest) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(m)
}

func run() error {
	micName := flag.String("mic", "KLIM", "case-insensitive capture device name substring")
	seconds := flag.Int("seconds", 60, "recording duration in seconds")
	out := flag.String("out", "", "output directory (default: %TEMP%\\echo-probe\\<UTC timestamp>)")
	list := flag.Bool("list", false, "list capture and playback devices, then exit")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flag.Args())
	}
	if !*list && (*seconds <= 0 || uint64(*seconds) > (uint64(^uint32(0))-36)/(sampleRate*2)) {
		return fmt.Errorf("-seconds must be positive and fit a PCM16 RIFF WAV")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, err := malgo.InitContext([]malgo.Backend{malgo.BackendWasapi}, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("WASAPI context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()
	captures, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return fmt.Errorf("capture enumeration: %w", err)
	}
	playbacks, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return fmt.Errorf("playback enumeration: %w", err)
	}
	if *list {
		for _, group := range []struct {
			name    string
			devices []malgo.DeviceInfo
		}{{"capture", captures}, {"playback", playbacks}} {
			for _, info := range group.devices {
				fmt.Printf("%s name=%q id=%s default=%t\n", group.name, info.Name(), info.ID.String(), info.IsDefault != 0)
			}
		}
		return nil
	}
	var micInfo, renderInfo malgo.DeviceInfo
	matches := 0
	for _, info := range captures {
		if strings.Contains(strings.ToLower(info.Name()), strings.ToLower(*micName)) {
			micInfo = info
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("-mic %q matched %d capture devices; use -list and a unique substring", *micName, matches)
	}
	foundDefault := false
	for _, info := range playbacks {
		if info.IsDefault != 0 {
			renderInfo, foundDefault = info, true
			break
		}
	}
	if !foundDefault {
		return fmt.Errorf("no default render endpoint found; use -list")
	}
	mic, micDevice, err := initStream(ctx.Context, malgo.Capture, micInfo)
	if err != nil {
		return fmt.Errorf("mic initialization: %w", err)
	}
	defer func() {
		if micDevice != nil {
			micDevice.Uninit()
		}
	}()
	render, renderDevice, err := initStream(ctx.Context, malgo.Loopback, renderInfo)
	if err != nil {
		return fmt.Errorf("render loopback initialization: %w", err)
	}
	defer func() {
		if renderDevice != nil {
			renderDevice.Uninit()
		}
	}()
	if *out == "" {
		*out = filepath.Join(os.TempDir(), "echo-probe", time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	if err := os.MkdirAll(*out, 0700); err != nil {
		return err
	}
	for _, name := range []string{"mic.wav", "render.wav", "manifest.json"} {
		if _, err := os.Lstat(filepath.Join(*out, name)); !os.IsNotExist(err) {
			return fmt.Errorf("output %s already exists or cannot be inspected", filepath.Join(*out, name))
		}
	}
	fmt.Fprintf(os.Stderr, "mic=%q id=%s\nrender=%q id=%s\nout=%s\n", mic.DeviceName, mic.DeviceID, render.DeviceName, render.DeviceID, *out)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	start := time.Now()
	ready := make(chan struct{})
	started := make(chan error, 2)
	go func() { <-ready; started <- mic.start(micDevice) }()
	go func() { <-ready; started <- render.start(renderDevice) }()
	close(ready)
	startErr := errors.Join(<-started, <-started)
	if startErr != nil {
		return fmt.Errorf("starting recording: %w", startErr)
	}
	timer := time.NewTimer(time.Until(start.Add(time.Duration(*seconds) * time.Second)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-interrupt:
	}
	stopped := make(chan error, 2)
	go func() { stopped <- micDevice.Stop() }()
	go func() { stopped <- renderDevice.Stop() }()
	recordingErr := errors.Join(<-stopped, <-stopped)
	micDevice.Uninit()
	renderDevice.Uninit()
	micDevice, renderDevice = nil, nil
	mic.finish()
	render.finish()
	end := time.Now()
	m := manifest{
		StartTime: start.UTC(), EndTime: end.UTC(), RequestedSeconds: *seconds,
		Seconds: end.Sub(start).Seconds(), SampleRate: sampleRate, Channels: 1, BitsPerSample: 16,
		Alignment: "Callback wall-clock estimates only; no hardware timestamps, gap padding, or drift correction. WAVs contain delivered frames only.",
		Mic:       mic, Render: render,
	}
	if mic.FirstCallbackUnixNano != 0 && render.FirstCallbackUnixNano != 0 {
		offset := mic.FirstCallbackUnixNano - render.FirstCallbackUnixNano
		m.FirstCallbackOffsetNano = &offset
	}
	recordingErr = errors.Join(recordingErr, writeWAV(filepath.Join(*out, "mic.wav"), mic.samples))
	recordingErr = errors.Join(recordingErr, writeWAV(filepath.Join(*out, "render.wav"), render.samples))
	recordingErr = errors.Join(recordingErr, writeManifest(filepath.Join(*out, "manifest.json"), m))
	delay, correlation := -1, 0.0
	if mic.FirstSampleUnixNano != 0 && render.FirstSampleUnixNano != 0 {
		delay, correlation = estimateDelay(mic.samples, render.samples, time.Duration(mic.FirstSampleUnixNano-render.FirstSampleUnixNano))
	}
	if len(mic.Gaps)+len(render.Gaps) > 0 {
		fmt.Fprintln(os.Stderr, "Warning: delivery gaps exceed 100 ms; delay is unreliable. See manifest.json.")
	}
	fmt.Printf("seconds=%.3f mic_rms=%.6f render_rms=%.6f mic_delay_ms=%d peak_correlation=%.6f\n", m.Seconds, pcmRMS(mic.samples), pcmRMS(render.samples), delay, correlation)
	for _, s := range []*stream{mic, render} {
		if s.Error != "" {
			recordingErr = errors.Join(recordingErr, fmt.Errorf("%s: %s", s.DeviceName, s.Error))
		}
		if s.Frames == 0 {
			recordingErr = errors.Join(recordingErr, fmt.Errorf("%s delivered no frames", s.DeviceName))
		}
	}
	return recordingErr
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "echo-probe:", err)
		os.Exit(1)
	}
}
