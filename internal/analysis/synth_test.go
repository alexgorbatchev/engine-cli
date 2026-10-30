package analysis

import (
	"math"
	"math/rand/v2"
)

// section renders bars of a four-on-the-floor pattern: a kick on every beat, an off-beat hi-hat, and a sustained
// chord that changes on each downbeat.
type section struct {
	bpm  float64
	bars int
}

var aMinorProgression = [][]float64{
	{220, 261.63, 329.63},            // A minor
	{146.83, 174.61, 220},            // D minor
	{164.81, 207.65, 246.94},         // E major, with the harmonic minor's G♯
	{220, 261.63, 329.63, 440},       // A minor
	{174.61, 220, 261.63},            // F major
	{146.83, 174.61, 220, 293.66},    // D minor
	{164.81, 207.65, 246.94, 329.63}, // E major
	{220, 261.63, 329.63},            // A minor
}

// synthesize renders the sections after offset seconds of silence and returns the samples and the time of every
// downbeat. Chords cycle through chords, one per bar.
func synthesize(offset float64, chords [][]float64, sections ...section) ([]float32, []float64) {
	rng := rand.New(rand.NewPCG(1, 2))
	var total float64 = offset
	for _, s := range sections {
		total += float64(s.bars*4) * 60 / s.bpm
	}
	samples := make([]float32, int((total+2)*SampleRate))
	add := func(at float64, length float64, voice func(t float64) float64) {
		start := int(at * SampleRate)
		for i := 0; i < int(length*SampleRate) && start+i < len(samples); i++ {
			samples[start+i] += float32(voice(float64(i) / SampleRate))
		}
	}
	var downbeats []float64
	t, bar := offset, 0
	for _, s := range sections {
		beat := 60 / s.bpm
		for range s.bars {
			downbeats = append(downbeats, t)
			chord := chords[bar%len(chords)]
			add(t, 4*beat, func(x float64) float64 {
				var v float64
				for _, f := range chord {
					v += math.Sin(2*math.Pi*f*x) + 0.3*math.Sin(4*math.Pi*f*x)
				}
				return 0.08 * v / float64(len(chord))
			})
			for k := range 4 {
				at := t + float64(k)*beat
				add(at, 0.25, func(x float64) float64 {
					return 0.6 * math.Exp(-x*18) * math.Sin(2*math.Pi*(50+90*math.Exp(-x*30))*x)
				})
				add(at+beat/2, 0.05, func(x float64) float64 {
					return 0.15 * math.Exp(-x*80) * (rng.Float64()*2 - 1)
				})
			}
			t += 4 * beat
			bar++
		}
	}
	return samples, downbeats
}
