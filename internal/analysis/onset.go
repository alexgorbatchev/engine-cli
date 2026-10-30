package analysis

import (
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	fftSize = 1024
	hop     = 128
	// bandEvery samples band energies every fourth onset frame, about 23 ms apart.
	bandEvery = 4
	bandCount = 12
	// lowCutoffHz bounds the kick-drum band that decides between on-beat and off-beat phases.
	lowCutoffHz = 180
)

// frameRate is the onset envelope's rate in frames per second.
const frameRate = float64(SampleRate) / hop

// onsetTracker computes a streaming short-time Fourier transform. Each frame contributes a broadband and a low-band
// half-wave rectified log spectral flux; every bandEvery frames it also records log energies in bands spaced
// logarithmically from 60 Hz to 8 kHz for downbeat novelty.
type onsetTracker struct {
	fft     *fourier.FFT
	window  []float64
	frame   []float64
	coeffs  []complex128
	prev    []float64
	pending []float32
	broad   []float32
	low     []float32
	bands   [][bandCount]float32
	edges   [bandCount + 1]int
	lowBins int
	frames  int
}

func newOnsetTracker() *onsetTracker {
	t := &onsetTracker{
		fft:    fourier.NewFFT(fftSize),
		window: make([]float64, fftSize),
		frame:  make([]float64, fftSize),
		coeffs: make([]complex128, fftSize/2+1),
		prev:   make([]float64, fftSize/2+1),
	}
	for i := range t.window {
		t.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}
	binHz := float64(SampleRate) / fftSize
	t.lowBins = int(math.Ceil(lowCutoffHz / binHz))
	for i := range t.edges {
		hz := 60 * math.Pow(8000.0/60, float64(i)/bandCount)
		t.edges[i] = min(fftSize/2, max(1, int(math.Round(hz/binHz))))
	}
	return t
}

func (t *onsetTracker) feed(samples []float32) {
	t.pending = append(t.pending, samples...)
	offset := 0
	for offset+fftSize <= len(t.pending) {
		t.process(t.pending[offset : offset+fftSize])
		offset += hop
	}
	t.pending = append(t.pending[:0], t.pending[offset:]...)
}

func (t *onsetTracker) process(samples []float32) {
	for i, s := range samples {
		t.frame[i] = float64(s) * t.window[i]
	}
	t.fft.Coefficients(t.coeffs, t.frame)
	var broad, low float64
	for k := 1; k < len(t.coeffs); k++ {
		mag := math.Log1p(cmplx.Abs(t.coeffs[k]))
		if t.frames > 0 {
			if d := mag - t.prev[k]; d > 0 {
				broad += d
				if k <= t.lowBins {
					low += d
				}
			}
		}
		t.prev[k] = mag
	}
	t.broad = append(t.broad, float32(broad))
	t.low = append(t.low, float32(low))
	if t.frames%bandEvery == 0 {
		var energies [bandCount]float32
		for b := range bandCount {
			var sum float64
			for k := t.edges[b]; k < max(t.edges[b]+1, t.edges[b+1]); k++ {
				m := cmplx.Abs(t.coeffs[k])
				sum += m * m
			}
			energies[b] = float32(math.Log(1e-9 + sum))
		}
		t.bands = append(t.bands, energies)
	}
	t.frames++
}

// onsetLatency corrects where log-compressed flux peaks relative to an attack: the largest increase arrives while
// the attack is still in the leading half of the window, before it reaches the frame centre. Measured on synthetic
// percussive attacks across 96–174 BPM and varied offsets, the peak leads the attack by 12.5 ms consistently.
const onsetLatency = 0.0125

// frameTime is the attack time attributed to the flux of onset frame i.
func frameTime(i float64) float64 {
	return (i*hop+fftSize/2)/SampleRate + onsetLatency
}

// frameAt is the fractional onset frame whose flux is attributed to time t.
func frameAt(t float64) float64 {
	return ((t-onsetLatency)*SampleRate - fftSize/2) / hop
}

// envelope combines broadband and low-band flux after removing their local means, so sustained loud passages do not
// outweigh quiet onsets and kick drums favour on-beat phases over off-beat hi-hats.
func (t *onsetTracker) envelope() []float64 {
	broad := detrend(t.broad)
	low := detrend(t.low)
	env := make([]float64, len(broad))
	for i := range env {
		env[i] = 0.5*broad[i] + low[i]
	}
	return env
}

// detrend subtracts a half-second moving average, rectifies, and normalizes the result to a unit mean.
func detrend(flux []float32) []float64 {
	out := make([]float64, len(flux))
	radius := int(math.Round(frameRate / 4))
	var sum float64
	lo, hi := 0, 0
	for i := range flux {
		for hi < len(flux) && hi <= i+radius {
			sum += float64(flux[hi])
			hi++
		}
		for lo < i-radius {
			sum -= float64(flux[lo])
			lo++
		}
		out[i] = math.Max(0, float64(flux[i])-sum/float64(hi-lo))
	}
	var mean float64
	for _, v := range out {
		mean += v
	}
	if mean <= 0 {
		return out
	}
	mean /= float64(len(out))
	for i := range out {
		out[i] /= mean
	}
	return out
}
