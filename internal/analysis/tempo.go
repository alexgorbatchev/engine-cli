package analysis

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/cabbagekobe/tactus"
)

const (
	// changeTolerance separates windows that disagree about tempo from estimator jitter.
	changeTolerance = 0.02
	// minSegmentWindows keeps tempo changes that last at least about a minute.
	minSegmentWindows = 2
	// fitSpan is the relative period range refined around a consensus tempo.
	fitSpan = 0.01
)

type tempoWindow struct {
	start, end float64
	candidates []tactus.Candidate
	err        error
}

func estimateWindowTempo(samples []float64, start, end, minBPM float64) tempoWindow {
	candidates, err := tactus.EstimateAllCandidatesWith(samples, SampleRate, tactus.Options{MinBPM: minBPM, MaxBPM: 2 * minBPM})
	return tempoWindow{start: start, end: end, candidates: candidates, err: err}
}

type span struct {
	start, end, bpm float64
}

// fitTempo turns window estimates into a beat grid: a consensus tempo, the tempo-change spans that windows agree on,
// a period and phase fitted to the onset envelope within each span, and the bar phase with the strongest novelty.
func (a *analyzer) fitTempo(ctx context.Context, duration float64) (*Tempo, error) {
	consensus, confidence, err := consensusBPM(a.tempos, a.options.minBPM())
	if err != nil {
		return nil, err
	}
	env := a.onsets.envelope()
	spans := tempoSpans(a.tempos, consensus)
	segments := make([]fittedSegment, 0, len(spans))
	for i, s := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Windows at a boundary mix both tempos; fit each segment away from them.
		fit := s
		if i > 0 && fit.end-fit.start > windowSeconds {
			fit.start += windowSeconds / 2
		}
		if i+1 < len(spans) && fit.end-fit.start > windowSeconds {
			fit.end -= windowSeconds / 2
		}
		segment := fitSegment(env, fit)
		segment.start, segment.end = s.start, s.end
		segments = append(segments, segment)
	}
	beats := joinSegments(env, segments)
	phase := barPhase(beats, a.onsets)
	anchors := barAnchors(beats, phase, duration)
	if len(anchors) < 2 {
		return nil, ErrNoBeat
	}
	first, last := anchors[0], anchors[len(anchors)-1]
	bpm := float64(last.Index-first.Index) / (last.Time - first.Time) * 60
	return &Tempo{BPM: bpm, Anchors: anchors, Confidence: confidence}, nil
}

// consensusBPM scores every window candidate with a narrow kernel and returns the best-supported tempo in the home
// octave, so a few windows with octave or metrical errors cannot outvote the rest.
func consensusBPM(windows []tempoWindow, minBPM float64) (float64, float64, error) {
	var tooShort, total int
	for _, w := range windows {
		total++
		if errors.Is(w.err, tactus.ErrTooShort) {
			tooShort++
		}
	}
	if total == 0 || tooShort == total {
		return 0, 0, ErrTooShort
	}
	best, bestScore, weights := 0.0, 0.0, 0.0
	for bpm := minBPM; bpm < 2*minBPM; bpm += 0.01 {
		var score float64
		for _, w := range windows {
			for _, c := range w.candidates {
				d := (c.BPM - bpm) / (0.003 * bpm)
				score += c.Confidence * c.Support * math.Exp(-0.5*d*d)
			}
		}
		if score > bestScore {
			best, bestScore = bpm, score
		}
	}
	for _, w := range windows {
		if len(w.candidates) > 0 {
			weights += w.candidates[0].Confidence
		}
	}
	if bestScore <= 0 || weights <= 0 {
		return 0, 0, ErrNoBeat
	}
	return best, math.Min(1, bestScore/weights), nil
}

// localBPM picks each window's candidate closest to the consensus, so octave alternates never read as tempo changes.
func localBPM(w tempoWindow, consensus float64) (float64, bool) {
	best, ok := 0.0, false
	for _, c := range w.candidates {
		if c.Confidence < 0.05 {
			continue
		}
		if !ok || math.Abs(c.BPM-consensus) < math.Abs(best-consensus) {
			best, ok = c.BPM, true
		}
	}
	return best, ok
}

// tempoSpans groups consecutive windows that agree on a tempo. Groups shorter than minSegmentWindows join their
// predecessor, so a single noisy window never introduces a tempo change.
func tempoSpans(windows []tempoWindow, consensus float64) []span {
	var spans []span
	var counts []int
	for _, w := range windows {
		bpm, ok := localBPM(w, consensus)
		if !ok {
			bpm = consensus
		}
		if n := len(spans); n > 0 && math.Abs(bpm/spans[n-1].bpm-1) <= changeTolerance {
			last := &spans[n-1]
			last.bpm = (last.bpm*float64(counts[n-1]) + bpm) / float64(counts[n-1]+1)
			last.end = w.end
			counts[n-1]++
			continue
		}
		spans = append(spans, span{start: w.start, end: w.end, bpm: bpm})
		counts = append(counts, 1)
	}
	var merged []span
	var mergedCounts []int
	for i, s := range spans {
		if n := len(merged); n > 0 && (counts[i] < minSegmentWindows || math.Abs(s.bpm/merged[n-1].bpm-1) <= changeTolerance) {
			merged[n-1].end = s.end
			mergedCounts[n-1] += counts[i]
			continue
		}
		merged = append(merged, s)
		mergedCounts = append(mergedCounts, counts[i])
	}
	if len(merged) > 1 && mergedCounts[0] < minSegmentWindows {
		merged[1].start = merged[0].start
		merged = merged[1:]
	}
	if len(merged) == 1 {
		merged[0].bpm = consensus
	}
	return merged
}

type fittedSegment struct {
	start, end float64
	period     float64
	reference  float64
}

// fitSegment searches periods within fitSpan of the span's tempo and phases across one period for the comb that
// collects the most onset energy, first coarsely and then around the best coarse fit.
func fitSegment(env []float64, s span) fittedSegment {
	lo := max(0, int(math.Floor(frameAt(s.start))))
	hi := min(len(env), int(math.Ceil(frameAt(s.end))))
	nominal := 60 / s.bpm * frameRate
	bestPeriod, bestPhase, bestScore := nominal, 0.0, -1.0
	search := func(periodLo, periodHi, periodStep, phaseLo, phaseHi, phaseStep float64) {
		for p := periodLo; p <= periodHi; p += periodStep {
			from, to := phaseLo, phaseHi
			if to < 0 {
				from, to = 0, p
			}
			for phase := from; phase < to; phase += phaseStep {
				if score := comb(env, lo, hi, phase, p); score > bestScore {
					bestPeriod, bestPhase, bestScore = p, phase, score
				}
			}
		}
	}
	// Over a whole track a 0.01% period error already moves the last beats by a frame, so the coarse pass keeps
	// that resolution and the fine pass refines period and phase together around it.
	search(nominal*(1-fitSpan), nominal*(1+fitSpan), nominal*0.0001, 0, -1, 0.25)
	coarsePeriod, coarsePhase := bestPeriod, bestPhase
	search(coarsePeriod*(1-0.00012), coarsePeriod*(1+0.00012), coarsePeriod*0.000005, coarsePhase-1, coarsePhase+1, 0.05)
	return fittedSegment{start: s.start, end: s.end, period: bestPeriod / frameRate, reference: frameTime(float64(lo) + bestPhase)}
}

// comb averages the envelope at every beat of the given phase and period within [lo, hi), interpolating between frames.
func comb(env []float64, lo, hi int, phase, period float64) float64 {
	var sum float64
	var n int
	for f := float64(lo) + phase; f < float64(hi-1); f += period {
		i := int(f)
		frac := f - float64(i)
		sum += env[i]*(1-frac) + env[i+1]*frac
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// joinSegments enumerates the beat anchors of consecutive segments. The first beat is the one nearest the start of the
// audio; each later segment begins at the change point between the two fitted grids, and the preceding segment
// keeps a whole number of beats up to it.
func joinSegments(env []float64, segments []fittedSegment) []Beat {
	first := segments[0]
	// A downbeat fitted a hair before the start still numbers the first bar.
	t := first.reference - math.Round(first.reference/first.period)*first.period
	beats := []Beat{{Time: t, Index: 0}}
	for i := 1; i < len(segments); i++ {
		prev := beats[len(beats)-1]
		start := changePoint(env, segments[i-1], segments[i], prev.Time)
		count := int64(math.Max(1, math.Round((start-prev.Time)/segments[i-1].period)))
		beats = append(beats, Beat{Time: start, Index: prev.Index + count})
	}
	// A final pseudo-anchor carries the last segment's period for enumeration and the tail.
	last := segments[len(segments)-1]
	end := beats[len(beats)-1]
	return append(beats, Beat{Time: end.Time + last.period, Index: end.Index + 1})
}

// changePoint chooses the beat of the next segment, within a window either side of the coarse boundary, where switching
// grids collects the most onset energy above the envelope's mean.
func changePoint(env []float64, before, after fittedSegment, earliest float64) float64 {
	var mean float64
	for _, v := range env {
		mean += v
	}
	mean /= float64(max(1, len(env)))
	value := func(t float64) float64 {
		f := frameAt(t)
		i := int(f)
		if f < 0 || i+1 >= len(env) {
			return 0
		}
		frac := f - float64(i)
		return env[i]*(1-frac) + env[i+1]*frac - mean
	}
	from := math.Max(earliest+before.period, after.start-windowSeconds)
	to := after.start + windowSeconds
	// Score every candidate against the same interval so each choice sums onsets over equal time.
	candidates := []float64{}
	for t := after.reference + math.Ceil((from-after.reference)/after.period)*after.period; t < to; t += after.period {
		candidates = append(candidates, t)
	}
	if len(candidates) == 0 {
		return after.reference + math.Ceil((after.start-after.reference)/after.period)*after.period
	}
	best, bestScore := candidates[0], math.Inf(-1)
	for _, c := range candidates {
		var score float64
		for t := before.reference + math.Ceil((from-before.reference)/before.period)*before.period; t < c-before.period/2; t += before.period {
			score += value(t)
		}
		for t := c; t < to; t += after.period {
			score += value(t)
		}
		if score > bestScore {
			best, bestScore = c, score
		}
	}
	return best
}

// beatTimes interpolates every beat between the joined anchors; the final pseudo-anchor repeats until limit.
func beatTimes(beats []Beat, limit float64) []float64 {
	var times []float64
	for i := 0; i+1 < len(beats); i++ {
		a, b := beats[i], beats[i+1]
		interval := (b.Time - a.Time) / float64(b.Index-a.Index)
		last := i+2 == len(beats)
		for k := int64(0); last || k < b.Index-a.Index; k++ {
			t := a.Time + float64(k)*interval
			if t > limit {
				break
			}
			times = append(times, t)
		}
	}
	return times
}

// barPhase chooses which of the first four beats starts a bar: the phase whose beats bring the largest change in band
// energies, since harmonic and arrangement changes land on downbeats.
func barPhase(beats []Beat, onsets *onsetTracker) int {
	limit := frameTime(float64(onsets.frames))
	times := beatTimes(beats, limit)
	if len(times) < 8 || len(onsets.bands) == 0 {
		return 0
	}
	means := make([][bandCount]float64, len(times)-1)
	for k := 0; k+1 < len(times); k++ {
		lo := max(0, int(frameAt(times[k]))/bandEvery)
		hi := min(len(onsets.bands), max(lo+1, int(frameAt(times[k+1]))/bandEvery))
		if lo >= len(onsets.bands) {
			means = means[:k]
			break
		}
		for f := lo; f < hi; f++ {
			for b := range bandCount {
				means[k][b] += float64(onsets.bands[f][b]) / float64(hi-lo)
			}
		}
	}
	var scores [4]float64
	for k := 1; k < len(means); k++ {
		var novelty float64
		for b := range bandCount {
			novelty += math.Abs(means[k][b] - means[k-1][b])
		}
		scores[k%4] += novelty
	}
	return slices.Index(scores[:], slices.Max(scores[:]))
}

// barAnchors reindexes the joined beats so beat 0 is the first downbeat, starts the grid one bar earlier as Engine's
// analyzer does, and ends it at least one second after the audio.
func barAnchors(beats []Beat, phase int, duration float64) []Beat {
	period := (beats[1].Time - beats[0].Time) / float64(beats[1].Index-beats[0].Index)
	downbeat := beats[0].Time + float64(phase)*period
	anchors := []Beat{{Time: downbeat - 4*period, Index: -4}}
	for _, b := range beats[1 : len(beats)-1] {
		anchors = append(anchors, Beat{Time: b.Time, Index: b.Index - int64(phase)})
	}
	tail := beats[len(beats)-1]
	last := anchors[len(anchors)-1]
	lastPeriod := tail.Time - beats[len(beats)-2].Time
	if len(anchors) == 1 {
		lastPeriod = period
	}
	count := int64(math.Max(1, math.Ceil((duration+1-last.Time)/lastPeriod)))
	return append(anchors, Beat{Time: last.Time + float64(count)*lastPeriod, Index: last.Index + count})
}
