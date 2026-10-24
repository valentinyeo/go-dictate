package audio

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func echoSignal(seed uint32, size int, amplitude float64) []float64 {
	signal := make([]float64, size)
	var previous, older float64
	for i := range signal {
		seed = seed*1664525 + 1013904223
		next := (float64(seed)/4294967296*2 - 1) * amplitude
		signal[i] = next*0.6 + previous*0.3 + older*0.1
		older, previous = previous, next
	}
	return signal
}

func echoPCM(signal []float64) []byte {
	pcm := make([]byte, len(signal)*2)
	for i, sample := range signal {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(math.Round(sample))))
	}
	return pcm
}

func echoStereo(left, right []float64) []byte {
	pcm := make([]byte, len(left)*4)
	for i := range left {
		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(int16(math.Round(left[i]))))
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(int16(math.Round(right[i]))))
	}
	return pcm
}

func echoRMS(signal []float64) float64 {
	var energy float64
	for _, sample := range signal {
		energy += sample * sample
	}
	return math.Sqrt(energy/float64(len(signal))) / 32768
}

func echoCapture(t *testing.T, left, right, mic []float64) ([]float64, float64) {
	t.Helper()
	chunks := make(chan []int16, 1)
	capture := &Capture{audioChunks: chunks, echo: &playbackReference{}}
	capture.echo.receive(echoStereo(left, right), uint32(len(left)), time.Now())
	capture.receiveFrames(nil, echoPCM(mic), uint32(len(mic)))
	select {
	case chunk := <-chunks:
		out := make([]float64, len(chunk))
		for i, sample := range chunk {
			out[i] = float64(sample)
		}
		return out, capture.PeakRMS()
	default:
		t.Fatal("capture did not deliver PCM")
		return nil, 0
	}
}

func TestCaptureEchoPlaybackOnly(t *testing.T) {
	for _, delay := range []int{0, 317, 913, 3200, 4700} {
		for _, gain := range []float64{0.2, 0.7, -0.4} {
			left := echoSignal(710, echoHistoryFrames, 13000)
			right := echoSignal(711, echoHistoryFrames, 11000)
			mic := make([]float64, 1280)
			for i := range mic {
				index := len(left) - len(mic) - delay + i
				mic[i] = gain * left[index]
				if delay < 3200 {
					mic[i] += 0.12 * right[index-107]
				}
			}
			out, peak := echoCapture(t, left, right, mic)
			t.Logf("delay=%d gain=%.2f mic_rms=%.6f output_rms=%.6f", delay, gain, echoRMS(mic), echoRMS(out))
			if echoRMS(out) > 0.002 {
				t.Errorf("playback reached VAD: residual RMS %.6f", echoRMS(out))
			}
			if math.Abs(peak-echoRMS(mic)) > 0.0001 {
				t.Errorf("watchdog lost raw mic energy: %.6f != %.6f", peak, echoRMS(mic))
			}
			vad := NewVAD(0.005, 1500)
			pcm := make([]int16, len(out))
			for i := range out {
				pcm[i] = int16(out[i])
			}
			if event := vad.Process(pcm); event != Silence {
				t.Errorf("playback opened speech: %s", event)
			}
		}
	}
}

func TestCaptureEchoSimultaneousVoice(t *testing.T) {
	for _, voiceLevel := range []float64{1300, 4500} {
		for _, delay := range []int{317, 913, 3200} {
			left := echoSignal(710, echoHistoryFrames, 13000)
			right := echoSignal(711, echoHistoryFrames, 11000)
			voice := echoSignal(2026, 1280, voiceLevel)
			mic := append([]float64(nil), voice...)
			for i := range mic {
				index := len(left) - len(mic) - delay + i
				mic[i] += 0.65*left[index] + 0.18*left[index-107] - 0.1*right[index-281]
			}
			out, _ := echoCapture(t, left, right, mic)
			errorSignal := make([]float64, len(out))
			var dot, voiceEnergy float64
			pcm := make([]int16, len(out))
			for i := range out {
				errorSignal[i] = out[i] - voice[i]
				dot += out[i] * voice[i]
				voiceEnergy += voice[i] * voice[i]
				pcm[i] = int16(out[i])
			}
			t.Logf("delay=%d voice_rms=%.6f mic_rms=%.6f output_rms=%.6f error_rms=%.6f voice_gain=%.4f", delay, echoRMS(voice), echoRMS(mic), echoRMS(out), echoRMS(errorSignal), dot/voiceEnergy)
			if echoRMS(errorSignal) > echoRMS(voice)*0.15 {
				t.Errorf("mixed output is not the user's voice: error RMS %.6f", echoRMS(errorSignal))
			}
			if gain := dot / voiceEnergy; gain < 0.95 || gain > 1.05 {
				t.Errorf("user speech changed: gain %.4f", gain)
			}
			if event := NewVAD(0.005, 1500).Process(pcm); event != SpeechStarted {
				t.Errorf("user speech did not open VAD: %s", event)
			}
		}
	}
}

func TestCaptureEchoVoiceWithoutPlaybackMatch(t *testing.T) {
	left := echoSignal(710, echoHistoryFrames, 13000)
	right := echoSignal(711, echoHistoryFrames, 11000)
	voice := echoSignal(2026, 1280, 4500)
	out, _ := echoCapture(t, left, right, voice)
	for i := range out {
		if out[i] != math.Round(voice[i]) {
			t.Fatalf("unrelated playback suppressed own voice at %d", i)
		}
	}
}

func TestEchoReferenceUnavailable(t *testing.T) {
	voice := echoSignal(2026, 1280, 4500)
	for _, age := range []time.Duration{0, time.Second} {
		r := &playbackReference{}
		if age > 0 {
			left := echoSignal(710, echoHistoryFrames, 13000)
			r.receive(echoStereo(left, left), uint32(len(left)), time.Now().Add(-age))
		}
		pcm := make([]int16, len(voice))
		for i := range pcm {
			pcm[i] = int16(math.Round(voice[i]))
		}
		r.filter(pcm, time.Now())
		for i, sample := range pcm {
			if sample != int16(math.Round(voice[i])) {
				t.Fatal("missing or stale reference changed speech")
			}
		}
	}
}

func TestEchoReferenceBounds(t *testing.T) {
	r := &playbackReference{}
	r.receive(nil, 1280, time.Now())
	if !r.updated.IsZero() {
		t.Fatal("invalid callback updated reference")
	}
	left := echoSignal(710, echoHistoryFrames*2, 13000)
	r.receive(echoStereo(left, left), uint32(len(left)), time.Now())
	for _, channel := range r.samples {
		if len(channel) != echoHistoryFrames || channel[0] != math.Round(left[len(left)-echoHistoryFrames]) {
			t.Fatal("reference did not retain the newest samples")
		}
	}
	r.filter(nil, time.Now())
}

func BenchmarkCaptureEcho(b *testing.B) {
	left := echoSignal(710, echoHistoryFrames, 13000)
	right := echoSignal(711, echoHistoryFrames, 11000)
	r := &playbackReference{}
	r.receive(echoStereo(left, right), uint32(len(left)), time.Now())
	mic := make([]int16, 1280)
	b.ResetTimer()
	for range b.N {
		for i := range mic {
			index := len(left) - len(mic) - 913 + i
			mic[i] = int16(0.6*left[index] + 0.2*right[index-317])
		}
		r.filter(mic, r.updated)
	}
}
