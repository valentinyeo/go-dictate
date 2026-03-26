package audio

import (
	"math"
	"sync/atomic"
	"time"
)

type VADEvent int

const (
	SpeechStarted   VADEvent = iota // Speech just started
	Speaking                        // Ongoing speech
	SilenceDetected                 // Silence threshold reached
	Silence                         // Ongoing silence
)

func (e VADEvent) String() string {
	switch e {
	case SpeechStarted:
		return "SpeechStarted"
	case Speaking:
		return "Speaking"
	case SilenceDetected:
		return "SilenceDetected"
	case Silence:
		return "Silence"
	default:
		return "Unknown"
	}
}

type VoiceActivityDetector struct {
	energyThreshold   float32
	silenceThreshold  time.Duration
	lastSpeechTime    *time.Time
	isSpeaking        bool
	lastRMS           atomic.Uint64 // last computed RMS (float64 bits) for diagnostics
}

func NewVAD(energyThreshold float32, silenceThresholdMs uint64) *VoiceActivityDetector {
	return &VoiceActivityDetector{
		energyThreshold:  energyThreshold,
		silenceThreshold: time.Duration(silenceThresholdMs) * time.Millisecond,
		lastSpeechTime:   nil,
		isSpeaking:       false,
	}
}

func (v *VoiceActivityDetector) Process(audioChunk []int16) VADEvent {
	isSpeech := v.detectSpeech(audioChunk)

	if isSpeech {
		now := time.Now()
		v.lastSpeechTime = &now

		if !v.isSpeaking {
			v.isSpeaking = true
			return SpeechStarted
		}
		return Speaking
	}

	// Silence detected
	if v.lastSpeechTime != nil {
		silenceDuration := time.Since(*v.lastSpeechTime)

		if v.isSpeaking && silenceDuration > v.silenceThreshold {
			v.isSpeaking = false
			return SilenceDetected
		}
	}

	if v.isSpeaking {
		return Speaking
	}
	return Silence
}

func (v *VoiceActivityDetector) detectSpeech(audioChunk []int16) bool {
	if len(audioChunk) == 0 {
		return false
	}

	// Calculate RMS energy
	var sumSquares float64
	for _, sample := range audioChunk {
		normalized := float64(sample) / float64(math.MaxInt16)
		sumSquares += normalized * normalized
	}

	rms := math.Sqrt(sumSquares / float64(len(audioChunk)))
	v.lastRMS.Store(math.Float64bits(rms))
	return float32(rms) > v.energyThreshold
}

// LastRMS returns the RMS energy of the most recently processed chunk.
func (v *VoiceActivityDetector) LastRMS() float64 {
	return math.Float64frombits(v.lastRMS.Load())
}

// IsSpeaking returns whether the VAD is currently in speaking state.
func (v *VoiceActivityDetector) IsSpeaking() bool {
	return v.isSpeaking
}

func (v *VoiceActivityDetector) Reset() {
	v.isSpeaking = false
	v.lastSpeechTime = nil
}

// ComputeRMS computes the RMS energy of an audio chunk (exported for pre-VAD filtering).
func ComputeRMS(audioChunk []int16) float64 {
	if len(audioChunk) == 0 {
		return 0
	}
	var sumSquares float64
	for _, sample := range audioChunk {
		normalized := float64(sample) / float64(math.MaxInt16)
		sumSquares += normalized * normalized
	}
	return math.Sqrt(sumSquares / float64(len(audioChunk)))
}
