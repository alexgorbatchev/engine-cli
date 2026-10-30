// Package analysis estimates tempo, beat positions, downbeats, and musical key from audio files.
//
// Decoding streams through FFmpeg at a fixed mono rate, so memory stays bounded for long mixes. Tempo candidates and
// key candidates come from tactus and mixedkey, evaluated over consecutive windows; beat placement, downbeats, and
// tempo-change segments are fitted here against a fine onset envelope of the whole track.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// SampleRate is the decoding rate shared by every estimator.
const SampleRate = 22050

const (
	windowSeconds    = 30
	minWindowSeconds = 10
)

var (
	// ErrTooShort reports audio too short to estimate tempo or key.
	ErrTooShort = errors.New("audio is too short to analyze")
	// ErrNoBeat reports audio without a detectable beat.
	ErrNoBeat = errors.New("no beat was detected")
	// ErrNoKey reports audio without a detectable key.
	ErrNoKey = errors.New("no musical key was detected")
)

// Options selects the estimates and the tempo range. Tempos fold into [MinBPM, 2×MinBPM).
type Options struct {
	Tempo  bool
	Key    bool
	MinBPM float64
}

// DefaultMinBPM folds tempos into 88–176 BPM.
const DefaultMinBPM = 88

// Validate rejects ranges outside the tempos DJ software analyzes.
func (o Options) Validate() error {
	if !o.Tempo && !o.Key {
		return errors.New("choose tempo or key analysis")
	}
	if o.MinBPM != 0 && (math.IsNaN(o.MinBPM) || o.MinBPM < 40 || o.MinBPM > 125) {
		return fmt.Errorf("the tempo range must start between 40 and 125 BPM")
	}
	return nil
}

func (o Options) minBPM() float64 {
	if o.MinBPM == 0 {
		return DefaultMinBPM
	}
	return o.MinBPM
}

// Beat is a grid anchor in seconds. Beat 0 is the first downbeat, and every fourth beat starts a bar.
type Beat struct {
	Time  float64 `json:"time"`
	Index int64   `json:"index"`
}

// Tempo is a fitted beat grid. Anchors start one bar before the first downbeat, add one anchor per tempo change, and
// end with an anchor at least one second past the end of the audio.
type Tempo struct {
	BPM        float64 `json:"bpm"`
	Anchors    []Beat  `json:"anchors"`
	Confidence float64 `json:"confidence"`
}

// Key is a detected key as Engine's stored index, which walks the circle of fifths from C major.
type Key struct {
	Index      int64   `json:"index"`
	Camelot    string  `json:"camelot"`
	Confidence float64 `json:"confidence"`
}

// Result holds every requested estimate. A nil estimate carries the reason in its error field.
type Result struct {
	Duration float64
	Tempo    *Tempo
	TempoErr error
	Key      *Key
	KeyErr   error
}

// File decodes and analyzes one audio file.
func File(ctx context.Context, path string, o Options) (Result, error) {
	if err := o.Validate(); err != nil {
		return Result{}, err
	}
	a := newAnalyzer(o)
	if err := decode(ctx, path, a.feed); err != nil {
		return Result{}, err
	}
	return a.finish(ctx)
}

// Samples analyzes mono samples at SampleRate. It shares File's estimators and serves decoded audio from other sources.
func Samples(ctx context.Context, samples []float32, o Options) (Result, error) {
	if err := o.Validate(); err != nil {
		return Result{}, err
	}
	a := newAnalyzer(o)
	for len(samples) > 0 {
		n := min(len(samples), SampleRate)
		if err := a.feed(samples[:n]); err != nil {
			return Result{}, err
		}
		samples = samples[n:]
	}
	return a.finish(ctx)
}

type analyzer struct {
	options Options
	onsets  *onsetTracker
	window  []float64
	start   int64
	total   int64
	tempos  []tempoWindow
	keys    []keyWindow
}

func newAnalyzer(o Options) *analyzer {
	return &analyzer{options: o, onsets: newOnsetTracker(), window: make([]float64, 0, windowSeconds*SampleRate)}
}

func (a *analyzer) feed(samples []float32) error {
	if a.options.Tempo {
		a.onsets.feed(samples)
	}
	for len(samples) > 0 {
		n := min(len(samples), cap(a.window)-len(a.window))
		for _, s := range samples[:n] {
			a.window = append(a.window, float64(s))
		}
		samples = samples[n:]
		a.total += int64(n)
		if len(a.window) == cap(a.window) {
			a.closeWindow()
		}
	}
	return nil
}

// closeWindow estimates the current window. A trailing window shorter than minWindowSeconds only contributes when the
// whole track is that short, so the estimate never rests on a fragment of a longer track.
func (a *analyzer) closeWindow() {
	if len(a.window) == 0 {
		return
	}
	start := float64(a.start) / SampleRate
	end := start + float64(len(a.window))/SampleRate
	if a.options.Tempo {
		a.tempos = append(a.tempos, estimateWindowTempo(a.window, start, end, a.options.minBPM()))
	}
	if a.options.Key {
		a.keys = append(a.keys, estimateWindowKey(a.window))
	}
	a.start += int64(len(a.window))
	a.window = a.window[:0]
}

func (a *analyzer) finish(ctx context.Context) (Result, error) {
	if len(a.window) >= minWindowSeconds*SampleRate || a.start == 0 {
		a.closeWindow()
	}
	result := Result{Duration: float64(a.total) / SampleRate}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if a.options.Tempo {
		result.Tempo, result.TempoErr = a.fitTempo(ctx, result.Duration)
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
	}
	if a.options.Key {
		result.Key, result.KeyErr = combineKeys(a.keys)
	}
	return result, nil
}
