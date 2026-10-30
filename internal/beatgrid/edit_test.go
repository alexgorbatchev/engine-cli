package beatgrid

import (
	"errors"
	"math"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/analysis"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

const rate = 44100

// steady is a 120 BPM grid (22050 samples per beat) with its first downbeat at one second, ending past ten seconds.
func steady() Source {
	anchors := []engine.BeatAnchor{
		{Sample: rate - 4*22050, Beat: -4, BeatsUntilNext: 24},
		{Sample: rate + 20*22050, Beat: 20},
	}
	return Source{SampleRate: rate, SampleLimit: 11 * rate, Default: anchors, Adjusted: anchors}
}

// changing switches from 120 to 100 BPM at beat 8.
func changing() Source {
	anchors := []engine.BeatAnchor{
		{Sample: 0, Beat: 0, BeatsUntilNext: 8},
		{Sample: 8 * 22050, Beat: 8, BeatsUntilNext: 10},
		{Sample: 8*22050 + 10*26460, Beat: 18},
	}
	return Source{SampleRate: rate, SampleLimit: 8*22050 + 10*26460, Default: anchors, Adjusted: anchors}
}

func apply(t *testing.T, source Source, draft []engine.BeatAnchor, e Edit) Grid {
	t.Helper()
	g, err := Apply(source, draft, e)
	if err != nil {
		t.Fatalf("%+v: %v", e, err)
	}
	if err := engine.ValidateBeatAnchors(g.Anchors); err != nil {
		t.Fatalf("%+v produced invalid anchors %+v: %v", e, g.Anchors, err)
	}
	if last := g.Anchors[len(g.Anchors)-1]; last.Sample < source.SampleLimit {
		t.Fatalf("%+v ends at %v before the limit %v", e, last.Sample, source.SampleLimit)
	}
	return g
}

func code(err error) string {
	var coded *progress.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// beatTime returns the time of a beat index under the grid.
func beatTime(g Grid, beat int64) float64 {
	for i := 0; i+1 < len(g.Anchors); i++ {
		a, b := g.Anchors[i], g.Anchors[i+1]
		if beat >= a.Beat && beat <= b.Beat {
			return (a.Sample + float64(beat-a.Beat)*(b.Sample-a.Sample)/float64(a.BeatsUntilNext)) / g.SampleRate
		}
	}
	return math.NaN()
}

func TestShiftMovesEveryBeatByFractionalSeconds(t *testing.T) {
	g := apply(t, steady(), nil, Edit{Kind: EditShift, Seconds: -0.0125})
	if got := beatTime(g, 0); math.Abs(got-0.9875) > 1e-9 {
		t.Fatalf("downbeat at %v", got)
	}
	if math.Abs(g.BPM-120) > 1e-9 {
		t.Fatalf("bpm=%v", g.BPM)
	}
}

func TestDownbeatEditsKeepBeatPositions(t *testing.T) {
	later := apply(t, steady(), nil, Edit{Kind: EditBarShift, Beats: 1})
	if got := beatTime(later, 0); math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("bar shift put beat 0 at %v", got)
	}
	// 2.6 s is nearest the beat at 2.5 s, three beats after the first downbeat.
	marked := apply(t, steady(), nil, Edit{Kind: EditDownbeat, Seconds: 2.6})
	if got := beatTime(marked, 0); math.Abs(math.Mod(got-2.5, 2)) > 1e-9 {
		t.Fatalf("downbeat at %v is not on a bar of 2.5 s", got)
	}
	same := apply(t, steady(), nil, Edit{Kind: EditDownbeat, Seconds: 3.05})
	if same.Anchors[0] != steady().Adjusted[0] {
		t.Fatalf("an existing downbeat moved: %+v", same.Anchors[0])
	}
}

func TestHalfAndDoubleKeepDownbeatsAndRoundTrip(t *testing.T) {
	doubled := apply(t, steady(), nil, Edit{Kind: EditScale, Factor: 2})
	if math.Abs(doubled.BPM-240) > 1e-9 || math.Abs(beatTime(doubled, 0)-1) > 1e-9 || math.Abs(beatTime(doubled, 4)-2) > 1e-9 {
		t.Fatalf("doubled=%+v", doubled)
	}
	halved := apply(t, steady(), doubled.Anchors, Edit{Kind: EditScale, Factor: 0.5})
	if math.Abs(halved.BPM-120) > 1e-9 || math.Abs(beatTime(halved, 0)-1) > 1e-9 || math.Abs(beatTime(halved, 4)-3) > 1e-9 {
		t.Fatalf("halved=%+v", halved)
	}
	// An odd number of beats in the final segment keeps the halved tempo exact.
	odd := steady()
	odd.Adjusted = []engine.BeatAnchor{{Sample: rate - 4*22050, Beat: -4, BeatsUntilNext: 25}, {Sample: rate + 21*22050, Beat: 21}}
	if g := apply(t, odd, nil, Edit{Kind: EditScale, Factor: 0.5}); math.Abs(g.BPM-60) > 1e-9 {
		t.Fatalf("odd halving bpm=%v", g.BPM)
	}
	twice := apply(t, steady(), halved.Anchors, Edit{Kind: EditScale, Factor: 0.5})
	if math.Abs(twice.BPM-60) > 1e-9 {
		t.Fatalf("repeated halving bpm=%v", twice.BPM)
	}
	unaligned := changing()
	unaligned.Adjusted = []engine.BeatAnchor{{Sample: 0, Beat: 0, BeatsUntilNext: 7}, {Sample: 7 * 22050, Beat: 7, BeatsUntilNext: 10}, {Sample: 7*22050 + 10*26460, Beat: 17}}
	if _, err := Apply(unaligned, nil, Edit{Kind: EditScale, Factor: 0.5}); code(err) != "beatgrid_half_unaligned" {
		t.Fatalf("err=%v", err)
	}
	if _, err := Apply(steady(), nil, Edit{Kind: EditScale, Factor: 3}); code(err) != "beatgrid_edit_invalid" {
		t.Fatalf("err=%v", err)
	}
	slow := apply(t, steady(), nil, Edit{Kind: EditTempo, BPM: 40})
	if _, err := Apply(steady(), slow.Anchors, Edit{Kind: EditScale, Factor: 0.5}); code(err) != "beatgrid_tempo_range" {
		t.Fatalf("err=%v", err)
	}
}

func TestTempoEditsMoveLaterMarkersWithTheSegment(t *testing.T) {
	g := apply(t, changing(), nil, Edit{Kind: EditTempo, Marker: 0, BPM: 96})
	// Eight beats at 96 BPM end at five seconds, where the 100 BPM segment now starts.
	if got := beatTime(g, 8); math.Abs(got-5) > 1e-9 {
		t.Fatalf("marker at %v", got)
	}
	if got := segmentBPM(rate, g.Anchors, 1); math.Abs(got-100) > 1e-9 {
		t.Fatalf("second segment bpm=%v", got)
	}
	for _, e := range []Edit{{Kind: EditTempo, Marker: 0, BPM: 29}, {Kind: EditTempo, Marker: 0, BPM: math.NaN()}} {
		if _, err := Apply(changing(), nil, e); code(err) != "beatgrid_tempo_range" {
			t.Fatalf("%+v err=%v", e, err)
		}
	}
	if _, err := Apply(changing(), nil, Edit{Kind: EditTempo, Marker: 2, BPM: 120}); code(err) != "beatgrid_edit_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestTempoMarkersAddMoveAndRemove(t *testing.T) {
	added := apply(t, steady(), nil, Edit{Kind: EditMarkerAdd, Seconds: 5.1})
	if len(added.Anchors) != 3 || added.Anchors[1].Beat != 8 || math.Abs(added.Anchors[1].Sample-5*rate) > 1e-6 {
		t.Fatalf("added=%+v", added.Anchors)
	}
	faster := apply(t, steady(), added.Anchors, Edit{Kind: EditTempo, Marker: 1, BPM: 128})
	if got := segmentBPM(rate, faster.Anchors, 1); math.Abs(got-128) > 1e-9 || math.Abs(beatTime(faster, 0)-1) > 1e-9 {
		t.Fatalf("faster=%+v", faster.Anchors)
	}
	moved := apply(t, steady(), faster.Anchors, Edit{Kind: EditMarkerMove, Marker: 1, Seconds: 5.2})
	if moved.Anchors[1].Beat != 8 || math.Abs(moved.Anchors[1].Sample-5.2*rate) > 1e-6 {
		t.Fatalf("moved=%+v", moved.Anchors)
	}
	// Removing the marker joins both segments at their average tempo from the unchanged first anchor.
	removed := apply(t, steady(), moved.Anchors, Edit{Kind: EditMarkerRemove, Marker: 1})
	merged := (moved.Anchors[2].Sample - moved.Anchors[0].Sample) / float64(moved.Anchors[2].Beat-moved.Anchors[0].Beat)
	if len(removed.Anchors) != 2 || removed.Anchors[0] != (engine.BeatAnchor{Sample: moved.Anchors[0].Sample, Beat: -4, BeatsUntilNext: removed.Anchors[0].BeatsUntilNext}) || math.Abs(segmentBPM(rate, removed.Anchors, 0)-rate*60/merged) > 1e-9 {
		t.Fatalf("removed=%+v", removed.Anchors)
	}
	for _, c := range []struct {
		edit Edit
		code string
	}{
		{Edit{Kind: EditMarkerAdd, Seconds: 5.05}, "beatgrid_marker_exists"},
		{Edit{Kind: EditMarkerAdd, Seconds: math.Inf(1)}, "beatgrid_edit_invalid"},
		{Edit{Kind: EditMarkerMove, Marker: 0, Seconds: 2}, "beatgrid_edit_invalid"},
		{Edit{Kind: EditMarkerMove, Marker: 1, Seconds: -2}, "beatgrid_edit_invalid"},
		// Twelve beats squeezed into 1.5 seconds would be 480 BPM.
		{Edit{Kind: EditMarkerMove, Marker: 1, Seconds: 0.5}, "beatgrid_tempo_range"},
		{Edit{Kind: EditMarkerRemove, Marker: 2}, "beatgrid_edit_invalid"},
	} {
		if _, err := Apply(steady(), added.Anchors, c.edit); code(err) != c.code {
			t.Fatalf("%+v err=%v", c.edit, err)
		}
	}
}

func TestTapTempoFitsSteadyTapsAndIgnoresOutliers(t *testing.T) {
	// 125 BPM taps near 3.02 s with jitter, a skipped tap, and a stray double tap after an earlier abandoned sequence.
	taps := []float64{0.2, 0.7, 3.02, 3.502, 3.98, 4.94, 5.0, 5.42, 5.9}
	g := apply(t, steady(), nil, Edit{Kind: EditTap, Taps: taps})
	if math.Abs(g.BPM-125) > 0.5 {
		t.Fatalf("bpm=%v", g.BPM)
	}
	// The first tap lands on the beat that was at 3 s, which keeps its bar position.
	if got := beatTime(g, 4); math.Abs(got-3.02) > 0.01 {
		t.Fatalf("beat 4 at %v", got)
	}
	for _, c := range []struct {
		taps []float64
		code string
	}{
		{[]float64{1, 2, 3}, "beatgrid_edit_invalid"},
		{[]float64{1, 1.5, 1.4, 2}, "beatgrid_edit_invalid"},
		{[]float64{1, 1.5, 5, 5.5, 6}, "beatgrid_edit_invalid"},
		{[]float64{1, 1.5, 2, 2.5, math.NaN()}, "beatgrid_edit_invalid"},
		{[]float64{1, 1.1, 1.2, 1.3, 1.4}, "beatgrid_tempo_range"},
		{[]float64{1, 1.5, 1.6, 2.6, 2.7}, "beatgrid_edit_invalid"},
	} {
		if _, err := Apply(steady(), nil, Edit{Kind: EditTap, Taps: c.taps}); code(err) != c.code {
			t.Fatalf("%v err=%v", c.taps, err)
		}
	}
	// Tapping a later segment moves its marker; the earlier segment absorbs the change.
	later := apply(t, changing(), nil, Edit{Kind: EditTap, Taps: []float64{6, 6.6, 7.2, 7.8, 8.4}})
	if got := segmentBPM(rate, later.Anchors, 1); math.Abs(got-100) > 1e-6 || later.Anchors[0].Sample != 0 {
		t.Fatalf("later=%+v", later.Anchors)
	}
	// 50 BPM taps eight beats into the second segment would start it before the grid's first anchor.
	if _, err := Apply(changing(), nil, Edit{Kind: EditTap, Taps: []float64{9, 10.2, 11.4, 12.6, 13.8}}); code(err) != "beatgrid_edit_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestResetRestoresTheDefaultGridAndDraftsAreValidated(t *testing.T) {
	source := steady()
	source.Adjusted = apply(t, source, nil, Edit{Kind: EditShift, Seconds: 0.25}).Anchors
	reset := apply(t, source, source.Adjusted, Edit{Kind: EditReset})
	if reset.Anchors[0] != source.Default[0] {
		t.Fatalf("reset=%+v", reset.Anchors)
	}
	for _, c := range []struct {
		draft []engine.BeatAnchor
		edit  Edit
		code  string
	}{
		{[]engine.BeatAnchor{{Sample: 0, BeatsUntilNext: 1}}, Edit{Kind: EditShift, Seconds: 1}, "beatgrid_invalid"},
		{nil, Edit{Kind: "warp"}, "beatgrid_edit_invalid"},
		{nil, Edit{Kind: EditShift}, "beatgrid_edit_invalid"},
		{nil, Edit{Kind: EditShift, Seconds: 61}, "beatgrid_edit_invalid"},
		{nil, Edit{Kind: EditBarShift, Beats: 4}, "beatgrid_edit_invalid"},
		{nil, Edit{Kind: EditDownbeat, Seconds: math.NaN()}, "beatgrid_edit_invalid"},
	} {
		if _, err := Apply(steady(), c.draft, c.edit); code(err) != c.code {
			t.Fatalf("%+v err=%v", c.edit, err)
		}
	}
}

func TestFromAnalysisCountsBeatsBetweenAnchors(t *testing.T) {
	anchors := FromAnalysis(analysis.Tempo{Anchors: []analysis.Beat{{Time: -2, Index: -4}, {Time: 30, Index: 60}, {Time: 61, Index: 122}}}, 48000)
	want := []engine.BeatAnchor{{Sample: -96000, Beat: -4, BeatsUntilNext: 64}, {Sample: 1440000, Beat: 60, BeatsUntilNext: 62}, {Sample: 2928000, Beat: 122}}
	for i := range want {
		if anchors[i] != want[i] {
			t.Fatalf("anchors=%+v", anchors)
		}
	}
}

func TestHalvingAOneBeatFinalSegmentEndsTheGridAtItsMarker(t *testing.T) {
	source := steady()
	// A marker one beat before the end of the grid, on an even beat.
	source.Adjusted = []engine.BeatAnchor{
		{Sample: rate - 4*22050, Beat: -4, BeatsUntilNext: 22},
		{Sample: rate + 18*22050, Beat: 18, BeatsUntilNext: 1},
		{Sample: rate + 19*22050, Beat: 19},
	}
	source.SampleLimit = 0
	g := apply(t, source, nil, Edit{Kind: EditScale, Factor: 0.5})
	if len(g.Anchors) != 2 || math.Abs(g.BPM-60) > 1e-9 {
		t.Fatalf("halved=%+v", g.Anchors)
	}
}

func TestEditsNeverEndBeforeTheGridDidWithoutASavedLimit(t *testing.T) {
	source := steady()
	source.SampleLimit = 0
	g := apply(t, source, nil, Edit{Kind: EditShift, Seconds: 0.01})
	if end := g.Anchors[len(g.Anchors)-1].Sample; end < source.Adjusted[1].Sample {
		t.Fatalf("grid ends at %v before %v", end, source.Adjusted[1].Sample)
	}
}

func TestDownbeatAndScaleEditsKeepEveryTempoSegment(t *testing.T) {
	shifted := apply(t, changing(), nil, Edit{Kind: EditBarShift, Beats: 1})
	for i, anchor := range shifted.Anchors[:2] {
		if anchor.Sample != changing().Adjusted[i].Sample || anchor.Beat != changing().Adjusted[i].Beat-1 {
			t.Fatalf("bar shift moved anchor %d: %+v", i, anchor)
		}
	}
	doubled := apply(t, changing(), nil, Edit{Kind: EditScale, Factor: 2})
	halved := apply(t, changing(), nil, Edit{Kind: EditScale, Factor: 0.5})
	for i, want := range []float64{120, 100} {
		if got := segmentBPM(rate, doubled.Anchors, i); math.Abs(got-2*want) > 1e-9 {
			t.Fatalf("doubled segment %d bpm=%v", i, got)
		}
		if got := segmentBPM(rate, halved.Anchors, i); math.Abs(got-want/2) > 1e-9 {
			t.Fatalf("halved segment %d bpm=%v", i, got)
		}
	}
	// The tempo change stays where it was.
	if doubled.Anchors[1].Sample != changing().Adjusted[1].Sample || halved.Anchors[1].Sample != changing().Adjusted[1].Sample {
		t.Fatalf("marker moved: %+v %+v", doubled.Anchors[1], halved.Anchors[1])
	}
}
