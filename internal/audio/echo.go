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
	delays  [2][]int
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
	var previousDelays [2][]int
	for channel := range reference {
		reference[channel] = append([]float64(nil), r.samples[channel]...)
		previousDelays[channel] = append([]int(nil), r.delays[channel]...)
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
	hasRender := false
	var energies [2][]float64
	for channel, render := range reference {
		spectra[channel] = make([]complex128, size)
		energies[channel] = make([]float64, len(render)+1)
		for i, sample := range render {
			spectra[channel][i] = complex(sample, 0)
			energies[channel][i+1] = energies[channel][i] + sample*sample
		}
		if energies[channel][len(render)] > 0 {
			hasRender = true
			echoFFT(spectra[channel], false)
		}
	}
	if !hasRender {
		return
	}
	var paths [][]float64
	var coefficients []float64
	var pathChannels, pathStarts []int
	addPath := func(channel, start int) {
		if start < 0 || start+len(samples) > len(reference[channel]) || len(paths) >= echoPaths*37 {
			return
		}
		candidate := reference[channel][start : start+len(samples)]
		for _, path := range paths {
			if &path[0] == &candidate[0] {
				return
			}
			var dot, energy, pathEnergy float64
			for i, sample := range candidate {
				dot += sample * path[i]
				energy += sample * sample
				pathEnergy += path[i] * path[i]
			}
			if energy == 0 || dot*dot >= energy*pathEnergy*(1-1e-10) {
				return
			}
		}
		paths = append(paths, candidate)
		pathChannels = append(pathChannels, channel)
		pathStarts = append(pathStarts, start)
	}
	for iteration := range echoPaths {
		residualSpectrum := make([]complex128, size)
		for i, sample := range residual {
			residualSpectrum[len(residual)-1-i] = complex(sample, 0)
		}
		echoFFT(residualSpectrum, false)
		bestScore := 0.0
		var bestRender []float64
		bestStart, bestChannel := -1, 0
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
					bestRender, bestStart, bestChannel = render, start, channel
				}
			}
		}
		var residualEnergy float64
		for _, sample := range residual {
			residualEnergy += sample * sample
		}
		if residualEnergy <= float64(len(samples)) || bestStart < 0 || bestScore < residualEnergy*0.15*0.15 {
			break
		}
		previousPaths := len(paths)
		if iteration == 0 && len(previousDelays[bestChannel]) > 0 {
			delay := len(bestRender) - len(samples) - bestStart
			nearest := previousDelays[bestChannel][0]
			for _, prior := range previousDelays[bestChannel] {
				if math.Abs(float64(delay-prior)) < math.Abs(float64(delay-nearest)) {
					nearest = prior
				}
			}
			shift := delay - nearest
			for channel, delays := range previousDelays {
				for _, prior := range delays {
					addPath(channel, len(reference[channel])-len(samples)-prior-shift)
				}
			}
		}
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
			addPath(bestChannel, start)
		}
		if len(paths) == previousPaths {
			break
		}
		fitted := fitEcho(paths, mic)
		if fitted == nil && previousPaths == 0 {
			paths, pathChannels, pathStarts = paths[:0], pathChannels[:0], pathStarts[:0]
			addPath(bestChannel, bestStart)
			fitted = fitEcho(paths, mic)
		}
		if fitted == nil {
			paths = paths[:previousPaths]
			pathChannels = pathChannels[:previousPaths]
			pathStarts = pathStarts[:previousPaths]
			break
		}
		coefficients = fitted
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
	var noiseEnergy float64
	for _, sample := range residual {
		noiseEnergy += sample * sample
	}
	noiseRMS := math.Sqrt(noiseEnergy / float64(len(samples)))
	r.mu.Lock()
	for channel := range r.delays {
		r.delays[channel] = r.delays[channel][:0]
	}
	for i, channel := range pathChannels {
		var energy float64
		for _, sample := range paths[i] {
			energy += sample * sample
		}
		if math.Abs(coefficients[i])*math.Sqrt(energy) <= 2*noiseRMS {
			continue
		}
		r.delays[channel] = append(r.delays[channel], len(reference[channel])-len(samples)-pathStarts[i])
	}
	r.mu.Unlock()
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
