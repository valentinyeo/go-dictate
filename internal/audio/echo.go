package audio

import (
	"math"
	"sync"
	"time"
)

const echoHistoryFrames = 16000 * 380 / 1000
const echoPaths = 8

type playbackReference struct {
	mu      sync.Mutex
	samples [2][]float64
	updated time.Time
}

func (r *playbackReference) receive(input []byte, frames uint32, now time.Time) {
	if uint64(len(input)) < uint64(frames)*4 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for channel := range r.samples {
		for i := 0; i < int(frames); i++ {
			index := i*4 + channel*2
			sample := int16(input[index]) | int16(input[index+1])<<8
			r.samples[channel] = append(r.samples[channel], float64(sample))
		}
		if len(r.samples[channel]) > echoHistoryFrames {
			copy(r.samples[channel], r.samples[channel][len(r.samples[channel])-echoHistoryFrames:])
			r.samples[channel] = r.samples[channel][:echoHistoryFrames]
		}
	}
	r.updated = now
}

func (r *playbackReference) filter(samples []int16, now time.Time) {
	r.mu.Lock()
	if r.updated.IsZero() || now.Sub(r.updated) > 380*time.Millisecond || len(samples) == 0 {
		r.mu.Unlock()
		return
	}
	var reference [2][]float64
	for channel := range reference {
		reference[channel] = append([]float64(nil), r.samples[channel]...)
	}
	r.mu.Unlock()

	mic := make([]float64, len(samples))
	var micEnergy float64
	for i, sample := range samples {
		mic[i] = float64(sample)
		micEnergy += mic[i] * mic[i]
	}
	if micEnergy == 0 {
		return
	}
	residual := append([]float64(nil), mic...)
	size := 1
	for size < len(reference[0])+len(samples) {
		size *= 2
	}
	var spectra [2][]complex128
	var energies [2][]float64
	for channel, render := range reference {
		spectra[channel] = make([]complex128, size)
		energies[channel] = make([]float64, len(render)+1)
		for i, sample := range render {
			spectra[channel][i] = complex(sample, 0)
			energies[channel][i+1] = energies[channel][i] + sample*sample
		}
		echoFFT(spectra[channel], false)
	}
	var paths [][]float64
	for range echoPaths {
		residualSpectrum := make([]complex128, size)
		for i, sample := range residual {
			residualSpectrum[len(residual)-1-i] = complex(sample, 0)
		}
		echoFFT(residualSpectrum, false)
		bestScore := 0.0
		var bestRender []float64
		bestStart := -1
		for channel, render := range reference {
			if len(render) < len(samples) || energies[channel][len(render)] == 0 {
				continue
			}
			correlation := make([]complex128, size)
			for i := range correlation {
				correlation[i] = spectra[channel][i] * residualSpectrum[i]
			}
			echoFFT(correlation, true)
			for start := 0; start+len(samples) <= len(render); start++ {
				dot := real(correlation[start+len(samples)-1])
				energy := energies[channel][start+len(samples)] - energies[channel][start]
				if energy > 0 && dot*dot/energy > bestScore {
					bestScore = dot * dot / energy
					bestRender, bestStart = render, start
				}
			}
		}
		var residualEnergy float64
		for _, sample := range residual {
			residualEnergy += sample * sample
		}
		if bestStart < 0 || bestScore < residualEnergy*0.15*0.15 {
			break
		}
		previousPaths := len(paths)
		begin, end := bestStart, bestStart
		for _, path := range paths {
			for start := max(0, bestStart-32); start <= min(len(bestRender)-len(samples), bestStart+32); start++ {
				if &path[0] == &bestRender[start] {
					begin = min(begin, max(0, start-32))
					end = max(end, min(len(bestRender)-len(samples), start+4))
				}
			}
		}
		for start := begin; start <= end; start++ {
			candidate := bestRender[start : start+len(samples)]
			duplicate := false
			for _, path := range paths {
				if &path[0] == &candidate[0] {
					duplicate = true
					break
				}
			}
			if !duplicate {
				paths = append(paths, candidate)
			}
		}
		if len(paths) == previousPaths {
			break
		}
		coefficients := fitEcho(paths, mic)
		if coefficients == nil {
			break
		}
		for i, sample := range mic {
			for j, path := range paths {
				sample -= coefficients[j] * path[i]
			}
			residual[i] = sample
		}
	}
	if len(paths) == 0 {
		return
	}
	for i, sample := range residual {
		samples[i] = int16(math.Round(max(-32768, min(32767, sample))))
	}
}

func fitEcho(paths [][]float64, mic []float64) []float64 {
	n := len(paths)
	matrix := make([][]float64, n)
	for i, path := range paths {
		matrix[i] = make([]float64, n+1)
		for k, sample := range path {
			matrix[i][n] += sample * mic[k]
			for j := range paths {
				matrix[i][j] += sample * paths[j][k]
			}
		}
	}
	for i := range paths {
		pivot := i
		for j := i + 1; j < n; j++ {
			if math.Abs(matrix[j][i]) > math.Abs(matrix[pivot][i]) {
				pivot = j
			}
		}
		matrix[i], matrix[pivot] = matrix[pivot], matrix[i]
		if math.Abs(matrix[i][i]) < 1e-6 {
			return nil
		}
		scale := matrix[i][i]
		for j := i; j <= n; j++ {
			matrix[i][j] /= scale
		}
		for j := range paths {
			if j == i {
				continue
			}
			scale := matrix[j][i]
			for k := i; k <= n; k++ {
				matrix[j][k] -= scale * matrix[i][k]
			}
		}
	}
	coefficients := make([]float64, n)
	for i := range coefficients {
		coefficients[i] = matrix[i][n]
	}
	return coefficients
}

func echoFFT(values []complex128, inverse bool) {
	n := len(values)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			values[i], values[j] = values[j], values[i]
		}
	}
	for width := 2; width <= n; width *= 2 {
		angle := -2 * math.Pi / float64(width)
		if inverse {
			angle = -angle
		}
		step := complex(math.Cos(angle), math.Sin(angle))
		for start := 0; start < n; start += width {
			phase := complex(1, 0)
			for offset := 0; offset < width/2; offset++ {
				a := values[start+offset]
				b := phase * values[start+offset+width/2]
				values[start+offset] = a + b
				values[start+offset+width/2] = a - b
				phase *= step
			}
		}
	}
	if inverse {
		for i := range values {
			values[i] /= complex(float64(n), 0)
		}
	}
}
