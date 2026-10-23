package diagnostics

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/valen/go-dictate/internal/logger"
)

const (
	sampleRate              = 16000
	maxStoredAudioSamples   = sampleRate * 60
	maxTranscriptPreviewLen = 500
)

type UtteranceConfig struct {
	Provider          string
	Model             string
	Language          string
	MicName           string
	ZeroLatency       bool
	SaverMode         bool
	NoiseMode         string
	ReusedConnection  bool
	ConnectionAge     time.Duration
	VADThreshold      float32
	StartRMS          float64
	PreRollChunks     int
	SaveFailedAudio   bool
	RecentReset       bool
	RecentResetReason string
	RecentResetAge    time.Duration
}

type Utterance struct {
	ID     uint64
	Config UtteranceConfig
	Start  time.Time
	Ended  bool

	Chunks       int
	Dropped      int
	Samples      int
	SumRMS       float64
	MaxRMS       float64
	TextEvents   int
	TextChars    int
	TextPreview  string
	previewTrunc bool
	FirstTextAt  time.Duration
	LastTextAt   time.Duration
	STTError     string
	storedAudio  []int16
	storedTrunc  bool
	endRequested string
}

type Tracker struct {
	mu      sync.Mutex
	nextID  uint64
	current *Utterance
}

func NewTracker() *Tracker {
	return &Tracker{}
}

func (t *Tracker) Begin(cfg UtteranceConfig) *Utterance {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current != nil && !t.current.Ended {
		reason := "interrupted_by_new_speech"
		if t.current.endRequested != "" {
			reason = t.current.endRequested
		}
		t.endLocked(t.current, reason)
	}

	t.nextID++
	u := &Utterance{
		ID:     t.nextID,
		Config: cfg,
		Start:  time.Now(),
	}
	t.current = u

	conn := "new"
	if cfg.ReusedConnection {
		conn = fmt.Sprintf("reused age=%s", cfg.ConnectionAge.Round(time.Millisecond))
	}
	reset := "none"
	if cfg.RecentReset {
		reset = fmt.Sprintf("%q age=%s", cfg.RecentResetReason, cfg.RecentResetAge.Round(time.Millisecond))
	}
	logger.Info("[UTT %d] start provider=%s model=%s lang=%s mic=%q zero_latency=%v saver=%v noise_mode=%s conn=%s vad_rms=%.4f threshold=%.4f preroll=%d recent_reset=%s",
		u.ID, cfg.Provider, cfg.Model, cfg.Language, cfg.MicName, cfg.ZeroLatency, cfg.SaverMode, cfg.NoiseMode, conn, cfg.StartRMS, cfg.VADThreshold, cfg.PreRollChunks, reset)

	return u
}

func (t *Tracker) RecordAudio(u *Utterance, chunk []int16) {
	if u == nil || len(chunk) == 0 {
		return
	}

	rms := chunkRMS(chunk)

	t.mu.Lock()
	defer t.mu.Unlock()
	if u.Ended {
		return
	}
	u.Chunks++
	u.Samples += len(chunk)
	u.SumRMS += rms
	if rms > u.MaxRMS {
		u.MaxRMS = rms
	}

	if u.Config.SaveFailedAudio && len(u.storedAudio) < maxStoredAudioSamples {
		remaining := maxStoredAudioSamples - len(u.storedAudio)
		if len(chunk) > remaining {
			u.storedAudio = append(u.storedAudio, chunk[:remaining]...)
			u.storedTrunc = true
		} else {
			u.storedAudio = append(u.storedAudio, chunk...)
		}
	} else if u.Config.SaveFailedAudio {
		u.storedTrunc = true
	}
}

func (t *Tracker) RecordDropped(u *Utterance) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !u.Ended {
		u.Dropped++
	}
}

func (t *Tracker) RecordText(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil || t.current.Ended {
		return
	}
	t.recordTextLocked(t.current, text)
}

func (t *Tracker) RecordTextFor(u *Utterance, text string) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if u.Ended {
		return
	}
	t.recordTextLocked(u, text)
}

func (t *Tracker) recordTextLocked(u *Utterance, text string) {
	clean := strings.ReplaceAll(text, "\x01", "")
	if clean == "" {
		return
	}

	now := time.Since(u.Start)
	if u.TextEvents == 0 {
		u.FirstTextAt = now
	}
	u.LastTextAt = now
	u.TextEvents++
	u.TextChars += len([]rune(clean))
	if len(u.TextPreview) < maxTranscriptPreviewLen {
		remaining := maxTranscriptPreviewLen - len(u.TextPreview)
		if len(clean) > remaining {
			for remaining > 0 && !utf8.RuneStart(clean[remaining]) {
				remaining--
			}
			u.TextPreview += clean[:remaining]
			u.previewTrunc = true
		} else {
			u.TextPreview += clean
		}
	}
}

func (t *Tracker) RecordSTTError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != nil && !t.current.Ended {
		t.current.STTError = err.Error()
	}
}

func (t *Tracker) RecordSTTErrorFor(u *Utterance, err error) {
	if u == nil || err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !u.Ended {
		u.STTError = err.Error()
	}
}

func (t *Tracker) RecordRecentReset(reason string, age time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != nil && !t.current.Ended {
		t.current.Config.RecentReset = true
		t.current.Config.RecentResetReason = reason
		t.current.Config.RecentResetAge = age
	}
}

func (t *Tracker) End(u *Utterance, reason string) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if u.Ended {
		return
	}
	t.endLocked(u, reason)
	if t.current == u {
		t.current = nil
	}
}

func (t *Tracker) EndCurrent(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil || t.current.Ended {
		return
	}
	u := t.current
	t.endLocked(u, reason)
	t.current = nil
}

func (t *Tracker) EndAfter(u *Utterance, reason string, delay time.Duration) {
	if u == nil {
		return
	}
	t.mu.Lock()
	if u.Ended {
		t.mu.Unlock()
		return
	}
	u.endRequested = reason
	t.mu.Unlock()

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		t.End(u, reason)
	}()
}

func (t *Tracker) endLocked(u *Utterance, reason string) {
	u.Ended = true
	duration := time.Since(u.Start)
	result := "ok"
	if u.TextChars == 0 {
		result = "empty"
	}

	avgRMS := 0.0
	if u.Chunks > 0 {
		avgRMS = u.SumRMS / float64(u.Chunks)
	}
	audioDuration := time.Duration(float64(u.Samples) / sampleRate * float64(time.Second))

	reset := "none"
	if u.Config.RecentReset {
		reset = fmt.Sprintf("%q age=%s", u.Config.RecentResetReason, u.Config.RecentResetAge.Round(time.Millisecond))
	}

	preview := strings.ReplaceAll(u.TextPreview, "\n", " ")
	if u.previewTrunc {
		preview += "…"
	}

	wavPath := ""
	if result != "ok" && u.Config.SaveFailedAudio && len(u.storedAudio) > 0 {
		path, err := saveFailedAudio(u)
		if err != nil {
			logger.Error("[UTT %d] failed to write diagnostic WAV: %v", u.ID, err)
		} else {
			wavPath = path
		}
	}

	extra := ""
	if u.STTError != "" {
		extra += fmt.Sprintf(" stt_error=%q", u.STTError)
	}
	if wavPath != "" {
		extra += fmt.Sprintf(" failed_wav=%q", wavPath)
		if u.storedTrunc {
			extra += " failed_wav_truncated=true"
		}
	} else if result != "ok" && !u.Config.SaveFailedAudio {
		extra += " failed_wav=disabled"
	}

	logger.Info("[UTT %d] result=%s reason=%s provider=%s model=%s duration=%s audio=%s chunks=%d dropped=%d avg_rms=%.4f max_rms=%.4f first_text=%s last_text=%s text_events=%d chars=%d recent_reset=%s text=%q%s",
		u.ID, result, reason, u.Config.Provider, u.Config.Model, duration.Round(time.Millisecond), audioDuration.Round(time.Millisecond), u.Chunks, u.Dropped,
		avgRMS, u.MaxRMS, formatTextDelay(u.FirstTextAt, u.TextEvents), formatTextDelay(u.LastTextAt, u.TextEvents), u.TextEvents, u.TextChars, reset, preview, extra)
}

func chunkRMS(chunk []int16) float64 {
	if len(chunk) == 0 {
		return 0
	}
	var sumSquares float64
	for _, sample := range chunk {
		normalized := float64(sample) / float64(math.MaxInt16)
		sumSquares += normalized * normalized
	}
	return math.Sqrt(sumSquares / float64(len(chunk)))
}

func formatTextDelay(d time.Duration, events int) string {
	if events == 0 {
		return "none"
	}
	return d.Round(time.Millisecond).String()
}

func saveFailedAudio(u *Utterance) (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA not set")
	}
	dir := filepath.Join(appData, "dictation", "failed-utterances")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("utt-%06d-%s.wav", u.ID, u.Start.Format("20060102-150405"))
	path := filepath.Join(dir, filename)
	if err := writePCM16WAV(path, u.storedAudio, sampleRate); err != nil {
		return "", err
	}
	return path, nil
}

func writePCM16WAV(path string, samples []int16, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dataSize := uint32(len(samples) * 2)
	byteRate := uint32(rate * 2)
	blockAlign := uint16(2)

	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(36)+dataSize); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(rate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, byteRate); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, blockAlign); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(16)); err != nil {
		return err
	}
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataSize); err != nil {
		return err
	}
	return binary.Write(f, binary.LittleEndian, samples)
}
