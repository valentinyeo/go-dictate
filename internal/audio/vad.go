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

// storeFloat64/loadFloat64 wrap the bit conversions needed to keep float64
// values in atomic.Uint64 fields.
func storeFloat64(a *atomic.Uint64, v float64) { a.Store(math.Float64bits(v)) }
func loadFloat64(a *atomic.Uint64) float64     { return math.Float64frombits(a.Load()) }

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

// VADOptions controls the noise-resistant speech gate.
type VADOptions struct {
	AdaptiveNoise     bool
	NoiseMultiplier   float32
	MinStartThreshold float32
	SpeechStartChunks int
}

type VoiceActivityDetector struct {
	now               func() time.Time
	lastChunkVoiced   bool
	energyThreshold   float32
	silenceThreshold  time.Duration
	lastSpeechTime    *time.Time
	isSpeaking        bool
	speechStartCount  int
	speechStartNeeded int
	adaptiveNoise     bool
	noiseMultiplier   float64
	minStartThreshold float64
	noiseFloor        float64
	noiseFloorReady   bool
	lastRMS           atomic.Uint64 // last computed RMS (float64 bits) for diagnostics
	lastThreshold     atomic.Uint64 // last effective threshold (float64 bits) for diagnostics
	lastNoiseFloor    atomic.Uint64 // last learned ambient RMS floor (float64 bits) for diagnostics
}

func NewVAD(energyThreshold float32, silenceThresholdMs uint64) *VoiceActivityDetector {
	return NewVADWithOptions(energyThreshold, silenceThresholdMs, VADOptions{})
}

func NewVADWithOptions(energyThreshold float32, silenceThresholdMs uint64, opts VADOptions) *VoiceActivityDetector {
	noiseMultiplier := float64(opts.NoiseMultiplier)
	if noiseMultiplier <= 0 {
		noiseMultiplier = 3.0
	}
	minStartThreshold := float64(opts.MinStartThreshold)
	if minStartThreshold < 0 {
		minStartThreshold = 0
	}
	speechStartChunks := opts.SpeechStartChunks
	if speechStartChunks <= 0 {
		speechStartChunks = 1
	}

	return &VoiceActivityDetector{
		now:               time.Now,
		energyThreshold:   energyThreshold,
		silenceThreshold:  time.Duration(silenceThresholdMs) * time.Millisecond,
		lastSpeechTime:    nil,
		isSpeaking:        false,
		adaptiveNoise:     opts.AdaptiveNoise,
		noiseMultiplier:   noiseMultiplier,
		minStartThreshold: minStartThreshold,
		speechStartNeeded: speechStartChunks,
	}
}

func (v *VoiceActivityDetector) Process(audioChunk []int16) VADEvent {
	rms := v.calculateRMS(audioChunk)
	storeFloat64(&v.lastRMS, rms)

	threshold := v.effectiveThreshold(v.isSpeaking)
	storeFloat64(&v.lastThreshold, threshold)
	isSpeech := rms > threshold
	v.lastChunkVoiced = isSpeech

	if isSpeech {
		if !v.isSpeaking {
			v.speechStartCount++
			if v.speechStartCount < v.requiredSpeechChunks() {
				// A single loud chunk is usually a key press, chair bump, or fan burst.
				// Return Silence so the chunk remains in the pre-roll buffer if this
				// becomes real speech on the following chunks.
				return Silence
			}
		}

		now := v.now()
		v.lastSpeechTime = &now

		if !v.isSpeaking {
			v.isSpeaking = true
			return SpeechStarted
		}
		return Speaking
	}

	v.speechStartCount = 0
	if !v.isSpeaking {
		v.updateNoiseFloor(rms)
	}

	// Silence detected
	if v.lastSpeechTime != nil {
		silenceDuration := v.now().Sub(*v.lastSpeechTime)

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

func (v *VoiceActivityDetector) calculateRMS(audioChunk []int16) float64 {
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

func (v *VoiceActivityDetector) effectiveThreshold(speaking bool) float64 {
	threshold := float64(v.energyThreshold)
	if !v.adaptiveNoise {
		return threshold
	}

	if v.minStartThreshold > threshold {
		threshold = v.minStartThreshold
	}
	if v.noiseFloorReady {
		adaptive := v.noiseFloor * v.noiseMultiplier
		if adaptive > threshold {
			threshold = adaptive
		}
	}

	if speaking {
		// Once speech has started, use a lower release threshold so quiet syllables
		// do not chop the utterance, while still letting fan noise fall back to silence.
		release := threshold * 0.60
		minRelease := float64(v.energyThreshold)
		if v.minStartThreshold > 0 && v.minStartThreshold*0.55 > minRelease {
			minRelease = v.minStartThreshold * 0.55
		}
		if release < minRelease {
			release = minRelease
		}
		return release
	}

	return threshold
}

func (v *VoiceActivityDetector) requiredSpeechChunks() int {
	if v.speechStartNeeded <= 0 {
		return 1
	}
	return v.speechStartNeeded
}

func (v *VoiceActivityDetector) updateNoiseFloor(rms float64) {
	if !v.adaptiveNoise {
		return
	}
	if rms < 0 {
		return
	}
	if !v.noiseFloorReady {
		v.noiseFloor = rms
		v.noiseFloorReady = true
		storeFloat64(&v.lastNoiseFloor, v.noiseFloor)
		return
	}

	// Do not let short non-speech spikes teach the noise floor upward.
	if rms > v.noiseFloor*3.0 && rms > v.minStartThreshold*0.75 {
		return
	}

	alpha := 0.04 // slow upward adaptation for fans/HVAC
	if rms < v.noiseFloor {
		alpha = 0.20 // faster recovery when the room gets quieter
	}
	v.noiseFloor = v.noiseFloor + alpha*(rms-v.noiseFloor)
	storeFloat64(&v.lastNoiseFloor, v.noiseFloor)
}

// LastRMS returns the RMS energy of the most recently processed chunk.
func (v *VoiceActivityDetector) LastRMS() float64 {
	return loadFloat64(&v.lastRMS)
}

// LastThreshold returns the effective threshold used for the most recent chunk.
func (v *VoiceActivityDetector) LastThreshold() float64 {
	return loadFloat64(&v.lastThreshold)
}

// NoiseFloor returns the learned ambient noise floor.
func (v *VoiceActivityDetector) NoiseFloor() float64 {
	return loadFloat64(&v.lastNoiseFloor)
}

func (v *VoiceActivityDetector) LastChunkVoiced() bool {
	return v.lastChunkVoiced
}

func (v *VoiceActivityDetector) Reset() {
	v.isSpeaking = false
	v.lastSpeechTime = nil
	v.speechStartCount = 0
}

// ChunkRMS returns the normalized RMS energy (0.0-1.0) of an int16 audio chunk.
// It is exported so the dictation loop can reason about ambient energy (echo
// gating and ducking hangover) without a second VAD instance.
func ChunkRMS(audioChunk []int16) float64 {
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
