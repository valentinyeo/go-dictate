package audio

import (
	"math"
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
	return float32(rms) > v.energyThreshold
}

func (v *VoiceActivityDetector) Reset() {
	v.isSpeaking = false
	v.lastSpeechTime = nil
}
