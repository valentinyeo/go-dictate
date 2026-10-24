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

func TestCaptureEchoColoredRoom(t *testing.T) {
	for _, decay := range []float64{0.4, 0.75, 0.9} {
		for _, voiceLevel := range []float64{0, 1300, 4500} {
			left := echoSignal(710, echoHistoryFrames, 13000)
			voice := echoSignal(2026, 1280, voiceLevel)
			mic := append([]float64(nil), voice...)
			taps := make([]float64, 32)
			var total float64
			for i := range taps {
				taps[i] = math.Pow(decay, float64(i)) * math.Cos(float64(i)*0.75)
				total += math.Abs(taps[i])
			}
			for i := range mic {
				for tap, gain := range taps {
					mic[i] += gain / total * 0.7 * left[len(left)-len(mic)-640-tap+i]
				}
			}
			out, _ := echoCapture(t, left, left, mic)
			errorSignal := make([]float64, len(out))
			var dot, voiceEnergy float64
			for i := range out {
				errorSignal[i] = out[i] - voice[i]
				dot += out[i] * voice[i]
				voiceEnergy += voice[i] * voice[i]
			}
			t.Logf("decay=%.2f voice_rms=%.6f mic_rms=%.6f error_rms=%.6f", decay, echoRMS(voice), echoRMS(mic), echoRMS(errorSignal))
			limit := 0.002
			if voiceLevel > 0 {
				limit = echoRMS(voice)*0.25 + 0.002
				if gain := dot / voiceEnergy; gain < 0.95 || gain > 1.05 {
					t.Errorf("colored echo changed voice gain: %.4f", gain)
				}
			}
			if echoRMS(errorSignal) > limit {
				t.Errorf("speaker/room frequency response was not removed: %.6f", echoRMS(errorSignal))
			}
		}
	}
}

func TestCaptureEchoStreamingTransitions(t *testing.T) {
	const frames = 1280
	left := echoSignal(710, frames*20, 13000)
	right := echoSignal(711, len(left), 11000)
	chunks := make(chan []int16, 1)
	capture := &Capture{audioChunks: chunks, echo: &playbackReference{}}
	vad := NewVAD(0.005, 1500)
	capture.echo.receive(echoStereo(left[:frames], right[:frames]), frames, time.Now())
	for block := 1; block < 20; block++ {
		begin, end := block*frames, (block+1)*frames
		for pos := begin; pos < end; pos += 160 {
			capture.echo.receive(echoStereo(left[pos:pos+160], right[pos:pos+160]), 160, time.Now())
		}
		voiceLevel := 0.0
		if block >= 4 && block < 12 {
			voiceLevel = 4500
		}
		voice := echoSignal(uint32(2026+block), frames, voiceLevel)
		mic := append([]float64(nil), voice...)
		gain := 0.65
		if block >= 6 && block < 11 {
			gain = 0.08
		}
		delay := 317
		if block >= 10 {
			delay = 629
		}
		for i := range mic {
			mic[i] += gain*left[begin-delay+i] + 0.12*right[begin-delay-107+i]
		}
		capture.receiveFrames(nil, echoPCM(mic), frames)
		out := <-chunks
		errorSignal := make([]float64, frames)
		for i, sample := range out {
			errorSignal[i] = float64(sample) - voice[i]
		}
		t.Logf("block=%d playback_gain=%.2f voice_rms=%.6f error_rms=%.6f", block, gain, echoRMS(voice), echoRMS(errorSignal))
		if echoRMS(errorSignal) > 0.004 {
			t.Errorf("playback restart, duck gain or delay change leaked: %.6f", echoRMS(errorSignal))
		}
		event := vad.Process(out)
		if block < 4 && event != Silence {
			t.Errorf("playback before user speech opened VAD: %s", event)
		}
		if block == 4 && event != SpeechStarted {
			t.Errorf("overlapping user speech did not open VAD: %s", event)
		}
	}
}

func TestEchoFFTCorrelationMatchesDirect(t *testing.T) {
	for _, frames := range []int{1, 160, 1280} {
		render := echoSignal(710, frames+317, 13000)
		mic := echoSignal(711, frames, 4500)
		size := 1
		for size < len(render)+len(mic) {
			size *= 2
		}
		x, y := make([]complex128, size), make([]complex128, size)
		for i, sample := range render {
			x[i] = complex(sample, 0)
		}
		for i, sample := range mic {
			y[len(mic)-1-i] = complex(sample, 0)
		}
		echoFFT(x, false)
		echoFFT(y, false)
		for i := range x {
			x[i] *= y[i]
		}
		echoFFT(x, true)
		for start := 0; start+frames <= len(render); start++ {
			var direct float64
			for i, sample := range mic {
				direct += sample * render[start+i]
			}
			if math.Abs(real(x[start+frames-1])-direct) > 0.01 {
				t.Fatalf("FFT correlation differs from direct at frames=%d start=%d", frames, start)
			}
		}
	}
}

func TestEchoStereoReferenceChange(t *testing.T) {
	chunks := make(chan []int16, 1)
	capture := &Capture{audioChunks: chunks, echo: &playbackReference{}}
	for step := range 3 {
		left := echoSignal(uint32(710+step), echoHistoryFrames, 13000)
		right := echoSignal(uint32(720+step), echoHistoryFrames, 11000)
		if step > 0 {
			for i := range right {
				right[i] = left[i]
				if step == 2 {
					right[i] = -right[i]
				}
			}
		}
		mic := make([]float64, 1280)
		for i := range mic {
			index := len(left) - len(mic) - 317 + i
			mic[i] = 0.45*left[index] + 0.15*right[index]
		}
		capture.echo.receive(echoStereo(left, right), uint32(len(left)), time.Now())
		capture.receiveFrames(nil, echoPCM(mic), uint32(len(mic)))
		out := <-chunks
		residual := make([]float64, len(out))
		for i, sample := range out {
			residual[i] = float64(sample)
		}
		if echoRMS(residual) > 0.002 {
			t.Errorf("stereo/mono/polarity transition leaked playback at step %d: %.6f", step, echoRMS(residual))
		}
	}
}

func TestEchoQuantizationHistory(t *testing.T) {
	chunks := make(chan []int16, 1)
	capture := &Capture{audioChunks: chunks, echo: &playbackReference{}}
	for block := range 5 {
		left := echoSignal(uint32(710+block), echoHistoryFrames, 13000)
		right := echoSignal(uint32(720+block), echoHistoryFrames, 11000)
		mic := make([]float64, 1280)
		for i := range mic {
			mic[i] = 0.65*left[len(left)-len(mic)-317+i] + 0.12*right[len(right)-len(mic)-424+i]
		}
		capture.echo.receive(echoStereo(left, right), uint32(len(left)), time.Now())
		capture.receiveFrames(nil, echoPCM(mic), uint32(len(mic)))
		<-chunks
		if taps := len(capture.echo.delays[0]) + len(capture.echo.delays[1]); taps != 2 {
			t.Errorf("learned quantization error as an acoustic response: %d taps", taps)
		}
	}
}

func TestEchoRememberedPathDuringDoubleTalk(t *testing.T) {
	const frames = 1280
	left := echoSignal(710, frames*10, 13000)
	right := echoSignal(711, len(left), 11000)
	chunks := make(chan []int16, 1)
	capture := &Capture{audioChunks: chunks, echo: &playbackReference{}}
	capture.echo.receive(echoStereo(left[:frames], right[:frames]), frames, time.Now())
	for block := 1; block <= 8; block++ {
		begin, end := block*frames, (block+1)*frames
		capture.echo.receive(echoStereo(left[begin:end], right[begin:end]), frames, time.Now())
		level, gain := 0.0, 0.65
		if block >= 4 {
			level = 4500
		}
		if block >= 6 {
			gain = 0.08
		}
		voice := echoSignal(uint32(2026+block), frames, level)
		mic := append([]float64(nil), voice...)
		for i := range mic {
			mic[i] += gain*left[begin-317+i] + 0.12*right[begin-424+i]
		}
		capture.receiveFrames(nil, echoPCM(mic), frames)
		<-chunks
		t.Logf("block=%d left_paths=%v right_paths=%v", block, capture.echo.delays[0], capture.echo.delays[1])
		for channel, expected := range []int{317, 424} {
			found := false
			for _, delay := range capture.echo.delays[channel] {
				found = found || delay == expected
			}
			if !found {
				t.Errorf("lost established channel %d path %d during double-talk at block %d", channel, expected, block)
			}
		}
	}
}
