package audio

import "math"

type HighPassFilter struct {
	alpha     float64
	previousX float64
	previousY float64
}

func NewHighPassFilter(sampleRate, cutoffHz float64) *HighPassFilter {
	if sampleRate <= 0 || cutoffHz <= 0 {
		return &HighPassFilter{}
	}
	dt := 1.0 / sampleRate
	rc := 1.0 / (2.0 * math.Pi * cutoffHz)
	return &HighPassFilter{alpha: rc / (rc + dt)}
}

func (f *HighPassFilter) ProcessInPlace(samples []int16) {
	if f == nil || f.alpha <= 0 || f.alpha >= 1 {
		return
	}
	for i, sample := range samples {
		x := float64(sample)
		y := f.alpha * (f.previousY + x - f.previousX)
		f.previousX = x
		f.previousY = y

		if y > math.MaxInt16 {
			y = math.MaxInt16
		} else if y < math.MinInt16 {
			y = math.MinInt16
		}
		samples[i] = int16(math.Round(y))
	}
}

func (f *HighPassFilter) Reset() {
	if f == nil {
		return
	}
	f.previousX = 0
	f.previousY = 0
}
