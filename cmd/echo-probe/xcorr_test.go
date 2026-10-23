package main

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func syntheticSignal(seed int64) []int16 {
	rng := rand.New(rand.NewSource(seed))
	samples := make([]int16, 1000*envelopeFrames)
	for frame := 0; frame < 1000; frame++ {
		amplitude := 100 + rng.Intn(20000)
		for i := 0; i < envelopeFrames; i++ {
			samples[frame*envelopeFrames+i] = int16(rng.Intn(2*amplitude+1) - amplitude)
		}
	}
	return samples
}

func TestEstimateDelay(t *testing.T) {
	render := syntheticSignal(710)
	mic := make([]int16, len(render)+12*envelopeFrames)
	copy(mic[12*envelopeFrames:], render)
	delay, correlation := estimateDelay(mic, render, 0)
	if delay != 120 || correlation <= 0.9 {
		t.Fatalf("delay=%d ms correlation=%f, want 120 ms and >0.9", delay, correlation)
	}
}

func TestEstimateDelayUnrelatedNoise(t *testing.T) {
	_, correlation := estimateDelay(syntheticSignal(710), syntheticSignal(711), 0)
	if correlation >= 0.3 {
		t.Fatalf("unrelated envelope correlation=%f, want <0.3", correlation)
	}
}

func TestEstimateDelayCallbackOffset(t *testing.T) {
	render := syntheticSignal(710)
	for _, offset := range []int{-6, 6} {
		mic := make([]int16, len(render)+(12-offset)*envelopeFrames)
		copy(mic[(12-offset)*envelopeFrames:], render)
		delay, correlation := estimateDelay(mic, render, time.Duration(offset)*10*time.Millisecond)
		if delay != 120 || correlation <= 0.9 {
			t.Fatalf("offset=%d: delay=%d correlation=%f", offset, delay, correlation)
		}
	}
}

func TestEstimateDelayRange(t *testing.T) {
	render := syntheticSignal(710)
	for _, lag := range []int{0, 30} {
		mic := make([]int16, len(render)+lag*envelopeFrames)
		copy(mic[lag*envelopeFrames:], render)
		delay, correlation := estimateDelay(mic, render, 0)
		if delay != lag*10 || correlation <= 0.9 {
			t.Fatalf("lag=%d: delay=%d correlation=%f", lag, delay, correlation)
		}
	}
}

func TestEstimateDelayNoEvidence(t *testing.T) {
	for _, samples := range [][]int16{nil, make([]int16, 160), make([]int16, sampleRate)} {
		delay, correlation := estimateDelay(samples, samples, 0)
		if delay != -1 || correlation != 0 {
			t.Fatalf("no evidence: delay=%d correlation=%f", delay, correlation)
		}
	}
}

func TestPCMRMS(t *testing.T) {
	if got := pcmRMS([]int16{-16384, 16384}); math.Abs(got-0.5) > 1e-12 {
		t.Fatalf("RMS=%f, want 0.5", got)
	}
	if got := pcmRMS(nil); got != 0 {
		t.Fatalf("empty RMS=%f, want zero", got)
	}
}
