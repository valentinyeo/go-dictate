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
	for i, sample := range samples {
		mic[i] = float64(sample)
	}
	residual := append([]float64(nil), mic...)
	var paths [][]float64
	for range echoPaths {
		bestScore := 0.0
		var best []float64
		for _, render := range reference {
			coarseScore, coarseStart := 0.0, -1
			for start := 0; start+len(samples) <= len(render); start++ {
				var dot, energy float64
				for i := 0; i < len(samples); i += 8 {
					x := render[start+i]
					dot += x * residual[i]
					energy += x * x
				}
				if energy > 0 && dot*dot/energy > coarseScore {
					coarseScore = dot * dot / energy
					coarseStart = start
				}
			}
			if coarseStart < 0 {
				continue
			}
			for start := max(0, coarseStart-8); start <= min(len(render)-len(samples), coarseStart+8); start++ {
				candidate := render[start : start+len(samples)]
				var dot, energy float64
				for i, x := range candidate {
					dot += x * residual[i]
					energy += x * x
				}
				if energy > 0 && dot*dot/energy > bestScore {
					bestScore = dot * dot / energy
					best = candidate
				}
			}
		}
		var residualEnergy float64
		for _, sample := range residual {
			residualEnergy += sample * sample
		}
		if best == nil || bestScore < residualEnergy*0.15*0.15 {
			break
		}
		paths = append(paths, best)
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
