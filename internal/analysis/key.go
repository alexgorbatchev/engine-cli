package analysis

import (
	"errors"
	"math"

	"github.com/alexgorbatchev/engine-cli/internal/musicalkey"
	"github.com/cabbagekobe/mixedkey"
)

type keyWindow struct {
	candidates []mixedkey.Candidate
	weight     float64
	err        error
}

// estimateWindowKey ranks all 24 keys for one window, weighted by its loudness so quiet intros and breakdowns
// count less than full arrangements.
func estimateWindowKey(samples []float64) keyWindow {
	candidates, err := mixedkey.DetectAllCandidates(samples, SampleRate)
	var energy float64
	for _, s := range samples {
		energy += s * s
	}
	return keyWindow{candidates: candidates, weight: math.Sqrt(energy / float64(len(samples))), err: err}
}

// combineKeys sums each key's correlation across windows and returns the strongest.
func combineKeys(windows []keyWindow) (*Key, error) {
	scores := map[mixedkey.Key]float64{}
	var total float64
	var tooShort, counted int
	for _, w := range windows {
		if errors.Is(w.err, mixedkey.ErrTooShort) {
			tooShort++
		}
		if w.err != nil || len(w.candidates) == 0 {
			continue
		}
		counted++
		total += w.weight
		for _, c := range w.candidates {
			scores[c.Key] += w.weight * c.Confidence
		}
	}
	if counted == 0 {
		if len(windows) > 0 && tooShort == len(windows) {
			return nil, ErrTooShort
		}
		return nil, ErrNoKey
	}
	var best mixedkey.Key
	bestScore := -1.0
	for key, score := range scores {
		// Ties resolve by Camelot text so repeated runs report the same key.
		if score > bestScore || score == bestScore && key.Camelot() < best.Camelot() {
			best, bestScore = key, score
		}
	}
	if bestScore <= 0 || total <= 0 {
		return nil, ErrNoKey
	}
	index := musicalkey.FromPitchClass(int(best.Tonic), best.Mode == mixedkey.Minor)
	return &Key{Index: index, Camelot: best.Camelot(), Confidence: math.Min(1, bestScore/total)}, nil
}
