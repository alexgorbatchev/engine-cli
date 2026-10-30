package beatgrid

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/nateranda/djtools/engine"
)

// Edit kinds.
const (
	EditShift        = "shift"
	EditBarShift     = "bar_shift"
	EditDownbeat     = "downbeat"
	EditScale        = "scale"
	EditTempo        = "tempo"
	EditMarkerAdd    = "marker_add"
	EditMarkerMove   = "marker_move"
	EditMarkerRemove = "marker_remove"
	EditTap          = "tap"
	EditReset        = "reset"
)

const (
	minTaps = 4
	maxTaps = 64
	// tapReset starts a new tap sequence after a pause, as tap-tempo controls do.
	tapReset = 2.0
	// tapOutlier discards intervals this far from the median, such as a doubled or hesitant tap.
	tapOutlier = 0.3
)

// Edit is one grid change. Times are seconds on the track's timeline; Marker indexes the grid's anchors.
type Edit struct {
	Kind    string    `json:"edit"`
	Seconds float64   `json:"seconds,omitempty"`
	Beats   int64     `json:"beats,omitempty"`
	Factor  float64   `json:"factor,omitempty"`
	BPM     float64   `json:"bpm,omitempty"`
	Marker  int       `json:"marker,omitempty"`
	Taps    []float64 `json:"taps,omitempty"`
}

// Source is the saved grid an edit starts from when no draft is supplied, and the grid a reset restores.
type Source struct {
	SampleRate  float64
	SampleLimit float64
	Default     []engine.BeatAnchor
	Adjusted    []engine.BeatAnchor
}

// SourceOf describes saved beat data for editing.
func SourceOf(data engine.BeatData) Source {
	return Source{SampleRate: data.SampleRate, SampleLimit: data.SampleLimit, Default: data.Default, Adjusted: data.Adjusted}
}

// Apply computes an edit on draft anchors, or on the saved adjusted grid when draft is nil, without saving it.
// Every result keeps beat indexes consistent, ends at least at the saved sample limit, and stays in Engine's tempo range.
func Apply(source Source, draft []engine.BeatAnchor, e Edit) (Grid, error) {
	base := draft
	if base == nil {
		base = source.Adjusted
	}
	if e.Kind == EditReset {
		base = source.Default
	}
	if err := Validate(source.SampleRate, base); err != nil {
		return Grid{}, err
	}
	g := grid{rate: source.SampleRate, limit: source.SampleLimit, anchors: slices.Clone(base)}
	var err error
	switch e.Kind {
	case EditReset:
	case EditShift:
		err = g.shift(e.Seconds)
	case EditBarShift:
		err = g.barShift(e.Beats)
	case EditDownbeat:
		err = g.downbeat(e.Seconds)
	case EditScale:
		err = g.scale(e.Factor)
	case EditTempo:
		err = g.tempo(e.Marker, e.BPM)
	case EditMarkerAdd:
		err = g.addMarker(e.Seconds)
	case EditMarkerMove:
		err = g.moveMarker(e.Marker, e.Seconds)
	case EditMarkerRemove:
		err = g.removeMarker(e.Marker)
	case EditTap:
		err = g.tap(e.Taps)
	default:
		return Grid{}, invalid("choose a supported grid edit")
	}
	if err != nil {
		return Grid{}, err
	}
	g.extendTail()
	if err := Validate(g.rate, g.anchors); err != nil {
		return Grid{}, err
	}
	return newGrid(g.rate, g.anchors), nil
}

func invalid(message string) error { return gridError("beatgrid_edit_invalid", message) }

type grid struct {
	rate    float64
	limit   float64
	anchors []engine.BeatAnchor
}

func finite(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func (g *grid) period(i int) float64 {
	return (g.anchors[i+1].Sample - g.anchors[i].Sample) / float64(g.anchors[i].BeatsUntilNext)
}

// segmentAt returns the segment containing sample, using the first or last segment outside the grid.
func (g *grid) segmentAt(sample float64) int {
	i := sort.Search(len(g.anchors), func(i int) bool { return g.anchors[i].Sample > sample }) - 1
	return min(max(i, 0), len(g.anchors)-2)
}

// nearestBeat returns the index and sample of the beat closest to sample.
func (g *grid) nearestBeat(sample float64) (int64, float64) {
	i := g.segmentAt(sample)
	p := g.period(i)
	offset := math.Round((sample - g.anchors[i].Sample) / p)
	return g.anchors[i].Beat + int64(offset), g.anchors[i].Sample + offset*p
}

// relink recomputes beat counts from beat indexes after anchors move or are reindexed.
func (g *grid) relink() {
	for i := 0; i+1 < len(g.anchors); i++ {
		g.anchors[i].BeatsUntilNext = uint32(max(0, g.anchors[i+1].Beat-g.anchors[i].Beat))
	}
	g.anchors[len(g.anchors)-1].BeatsUntilNext = 0
}

// extendTail places the final anchor at the first beat of the last segment at or past the sample limit, as Engine
// extends and trims grid tails. The last segment keeps its tempo.
func (g *grid) extendTail() {
	n := len(g.anchors)
	last := g.anchors[n-2]
	p := g.period(n - 2)
	count := math.Max(1, math.Ceil((g.limit-last.Sample)/p))
	g.anchors[n-1] = engine.BeatAnchor{Sample: last.Sample + count*p, Beat: last.Beat + int64(count)}
	g.anchors[n-2].BeatsUntilNext = uint32(count)
}

func (g *grid) shift(seconds float64) error {
	if !finite(seconds) || seconds == 0 || math.Abs(seconds) > 60 {
		return invalid("shift the grid by up to 60 seconds")
	}
	for i := range g.anchors {
		g.anchors[i].Sample += seconds * g.rate
	}
	return nil
}

// barShift moves every downbeat by whole beats; positive values move downbeats later.
func (g *grid) barShift(beats int64) error {
	if beats == 0 || beats < -3 || beats > 3 {
		return invalid("move downbeats by one to three beats")
	}
	for i := range g.anchors {
		g.anchors[i].Beat -= beats
	}
	return nil
}

// downbeat makes the beat nearest seconds start a bar, keeping every beat position.
func (g *grid) downbeat(seconds float64) error {
	if !finite(seconds) {
		return invalid("choose a downbeat position")
	}
	beat, _ := g.nearestBeat(seconds * g.rate)
	offset := (beat%4 + 4) % 4
	if offset == 0 {
		return nil
	}
	return g.barShift(offset)
}

// scale doubles or halves the tempo of every segment. Beat 0 and the anchors stay in place, so existing downbeats
// remain downbeats when doubling and every other one remains when halving.
func (g *grid) scale(factor float64) error {
	switch factor {
	case 2:
		for i := range g.anchors {
			g.anchors[i].Beat *= 2
		}
	case 0.5:
		for _, a := range g.anchors[:len(g.anchors)-1] {
			if a.Beat%2 != 0 {
				return gridError("beatgrid_half_unaligned", "a tempo marker sits between the beats of the halved tempo; move or remove it first")
			}
		}
		// An odd final segment ends one old beat earlier, keeping its tempo; extendTail places the final anchor again.
		n := len(g.anchors)
		if (g.anchors[n-1].Beat-g.anchors[n-2].Beat)%2 != 0 {
			g.anchors[n-1].Sample -= g.period(n - 2)
			g.anchors[n-1].Beat--
		}
		for i := range g.anchors {
			g.anchors[i].Beat /= 2
		}
	default:
		return invalid("double or halve the tempo")
	}
	g.relink()
	return nil
}

// tempo sets one segment's BPM from its starting anchor; later anchors move with the segment's end.
func (g *grid) tempo(marker int, bpm float64) error {
	if marker < 0 || marker > len(g.anchors)-2 {
		return invalid("choose a tempo segment")
	}
	if !finite(bpm) || bpm < MinBPM || bpm > MaxBPM {
		return gridError("beatgrid_tempo_range", fmt.Sprintf("choose a tempo between %d and %d BPM", MinBPM, MaxBPM))
	}
	g.retime(marker, g.anchors[marker].Sample, g.rate*60/bpm)
	return nil
}

// retime places segment i at start with period p and moves later anchors by the change in its end.
func (g *grid) retime(i int, start, p float64) {
	end := start + float64(g.anchors[i].BeatsUntilNext)*p
	delta := end - g.anchors[i+1].Sample
	g.anchors[i].Sample = start
	for j := i + 1; j < len(g.anchors); j++ {
		g.anchors[j].Sample += delta
	}
}

// addMarker inserts a tempo marker on the beat nearest seconds, keeping the tempo on both sides.
func (g *grid) addMarker(seconds float64) error {
	if !finite(seconds) {
		return invalid("choose a marker position")
	}
	beat, sample := g.nearestBeat(seconds * g.rate)
	i := g.segmentAt(sample)
	if beat <= g.anchors[i].Beat || beat >= g.anchors[i+1].Beat {
		return gridError("beatgrid_marker_exists", "choose a beat between existing tempo markers")
	}
	g.anchors = slices.Insert(g.anchors, i+1, engine.BeatAnchor{Sample: sample, Beat: beat})
	g.relink()
	return nil
}

func (g *grid) innerMarker(marker int) error {
	if marker <= 0 || marker >= len(g.anchors)-1 {
		return invalid("choose a tempo marker after the start of the grid")
	}
	return nil
}

// moveMarker moves a tempo marker between its neighbours; both adjacent segments keep their beat counts.
func (g *grid) moveMarker(marker int, seconds float64) error {
	if err := g.innerMarker(marker); err != nil {
		return err
	}
	sample := seconds * g.rate
	if !finite(seconds) || sample <= g.anchors[marker-1].Sample || sample >= g.anchors[marker+1].Sample {
		return invalid("keep the marker between its neighbouring markers")
	}
	g.anchors[marker].Sample = sample
	return nil
}

// removeMarker joins the segments either side of a marker at their combined average tempo.
func (g *grid) removeMarker(marker int) error {
	if err := g.innerMarker(marker); err != nil {
		return err
	}
	g.anchors = slices.Delete(g.anchors, marker, marker+1)
	g.relink()
	return nil
}

// tap fits a tempo and beat phase to tap times. Taps after a pause of tapReset start a new sequence and only the
// latest counts; intervals far from the median are dropped, and a skipped tap counts as two beats. A least-squares
// line through the numbered taps gives the period and the first tap's beat, which keeps the index of the nearest
// existing beat so bars stay where they were.
func (g *grid) tap(taps []float64) error {
	if len(taps) < minTaps || len(taps) > maxTaps || !finite(taps...) {
		return invalid(fmt.Sprintf("tap between %d and %d beats", minTaps, maxTaps))
	}
	start := 0
	for i := 1; i < len(taps); i++ {
		if taps[i] <= taps[i-1] {
			return invalid("tap times must increase")
		}
		if taps[i]-taps[i-1] > tapReset {
			start = i
		}
	}
	taps = taps[start:]
	if len(taps) < minTaps {
		return invalid(fmt.Sprintf("tap at least %d beats without pausing", minTaps))
	}
	intervals := make([]float64, len(taps)-1)
	for i := range intervals {
		intervals[i] = taps[i+1] - taps[i]
	}
	median := slices.Clone(intervals)
	slices.Sort(median)
	m := median[len(median)/2]
	numbers := []float64{0}
	times := []float64{taps[0]}
	for _, tap := range taps[1:] {
		// Measure from the last accepted tap so a dropped stray tap does not distort the next interval.
		d := tap - times[len(times)-1]
		beats := math.Round(d / m)
		if beats < 1 || math.Abs(d/beats-m) > tapOutlier*m {
			continue
		}
		numbers = append(numbers, numbers[len(numbers)-1]+beats)
		times = append(times, tap)
	}
	if len(times) < minTaps {
		return invalid("tap more steadily")
	}
	var meanN, meanT float64
	for i := range numbers {
		meanN += numbers[i]
		meanT += times[i]
	}
	meanN /= float64(len(numbers))
	meanT /= float64(len(numbers))
	var num, den float64
	for i := range numbers {
		num += (numbers[i] - meanN) * (times[i] - meanT)
		den += (numbers[i] - meanN) * (numbers[i] - meanN)
	}
	period := num / den
	first := meanT - period*meanN
	bpm := 60 / period
	if !finite(bpm) || bpm < MinBPM || bpm > MaxBPM {
		return gridError("beatgrid_tempo_range", fmt.Sprintf("taps measured %.1f BPM; tap between %d and %d BPM", bpm, MinBPM, MaxBPM))
	}
	sample := first * g.rate
	i := g.segmentAt(sample)
	beat, _ := g.nearestBeat(sample)
	p := g.rate * period
	start0 := sample - float64(beat-g.anchors[i].Beat)*p
	if i > 0 && start0 <= g.anchors[i-1].Sample {
		return invalid("the tapped tempo would move this segment past the previous marker")
	}
	g.retime(i, start0, p)
	return nil
}
