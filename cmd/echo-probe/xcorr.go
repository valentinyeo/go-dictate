package main

import (
	"math"
	"time"
)

const sampleRate = 16000
const envelopeFrames = sampleRate / 100

func pcmRMS(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var energy float64
	for _, sample := range samples {
		v := float64(sample) / 32768
		energy += v * v
	}
	return math.Sqrt(energy / float64(len(samples)))
}

func rmsEnvelope(samples []int16) []float64 {
	envelope := make([]float64, len(samples)/envelopeFrames)
	for i := range envelope {
		envelope[i] = pcmRMS(samples[i*envelopeFrames : (i+1)*envelopeFrames])
	}
	return envelope
}

func estimateDelay(mic, render []int16, micOffset time.Duration) (int, float64) {
	m, r := rmsEnvelope(mic), rmsEnvelope(render)
	offset := int(math.Round(float64(micOffset) / float64(10*time.Millisecond)))
	bestDelay, peak := -1, 0.0
	for lag := 0; lag <= 30; lag++ {
		begin := max(0, lag-offset)
		end := min(len(m), len(r)+lag-offset)
		if end-begin < 50 {
			continue
		}
		x, y := m[begin:end], r[begin+offset-lag:end+offset-lag]
		var meanX, meanY float64
		for i := range x {
			meanX += x[i]
			meanY += y[i]
		}
		meanX /= float64(len(x))
		meanY /= float64(len(y))
		var covariance, energyX, energyY float64
		for i := range x {
			dx, dy := x[i]-meanX, y[i]-meanY
			covariance += dx * dy
			energyX += dx * dx
			energyY += dy * dy
		}
		if energyX < 1e-12 || energyY < 1e-12 {
			continue
		}
		correlation := max(-1, min(1, covariance/math.Sqrt(energyX*energyY)))
		if correlation > peak {
			bestDelay, peak = lag*10, correlation
		}
	}
	return bestDelay, peak
}
