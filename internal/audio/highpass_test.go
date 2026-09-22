package audio

import (
	"math"
	"testing"
)

func sineWave(frequency float64, samples int) []int16 {
	result := make([]int16, samples)
	for i := range result {
		result[i] = int16(12000 * math.Sin(2*math.Pi*frequency*float64(i)/16000))
	}
	return result
}

func rms(samples []int16) float64 {
	var sum float64
	for _, sample := range samples {
		x := float64(sample)
		sum += x * x
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func TestHighPassFilterAttenuatesRumbleAndPreservesSpeechBand(t *testing.T) {
	const skipTransient = 1600

	rumble := sineWave(40, 16000)
	rumbleInputRMS := rms(rumble[skipTransient:])
	NewHighPassFilter(16000, 120).ProcessInPlace(rumble)
	rumbleRatio := rms(rumble[skipTransient:]) / rumbleInputRMS

	speech := sineWave(500, 16000)
	speechInputRMS := rms(speech[skipTransient:])
	NewHighPassFilter(16000, 120).ProcessInPlace(speech)
	speechRatio := rms(speech[skipTransient:]) / speechInputRMS

	if rumbleRatio >= 0.40 {
		t.Fatalf("40 Hz rumble ratio = %.3f, want < 0.40", rumbleRatio)
	}
	if speechRatio <= 0.90 {
		t.Fatalf("500 Hz speech-band ratio = %.3f, want > 0.90", speechRatio)
	}
}

func TestHighPassFilterDisabledForInvalidParameters(t *testing.T) {
	samples := []int16{100, -200, 300}
	NewHighPassFilter(0, 120).ProcessInPlace(samples)
	want := []int16{100, -200, 300}
	for i := range samples {
		if samples[i] != want[i] {
			t.Fatalf("sample %d = %d, want %d", i, samples[i], want[i])
		}
	}
}
