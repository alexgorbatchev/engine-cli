package analysis

import (
	"context"
	"errors"
	"math"
	"testing"
)

// beatAt interpolates the grid time of a beat index between anchors.
func beatAt(anchors []Beat, index int64) float64 {
	for i := 0; i+1 < len(anchors); i++ {
		a, b := anchors[i], anchors[i+1]
		if index >= a.Index && index <= b.Index {
			return a.Time + float64(index-a.Index)*(b.Time-a.Time)/float64(b.Index-a.Index)
		}
	}
	return math.NaN()
}

func TestConstantTempoFindsTempoDownbeatsAndKey(t *testing.T) {
	samples, downbeats := synthesize(0.37, aMinorProgression, section{bpm: 124.6, bars: 64})
	result, err := Samples(context.Background(), samples, Options{Tempo: true, Key: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.TempoErr != nil || result.KeyErr != nil {
		t.Fatalf("tempo=%v key=%v", result.TempoErr, result.KeyErr)
	}
	tempo := result.Tempo
	if math.Abs(tempo.BPM-124.6) > 0.02 {
		t.Fatalf("bpm=%.4f", tempo.BPM)
	}
	if len(tempo.Anchors) != 2 || tempo.Anchors[0].Index != -4 {
		t.Fatalf("anchors=%+v", tempo.Anchors)
	}
	if end := tempo.Anchors[1].Time; end < result.Duration+1 {
		t.Fatalf("grid ends at %.2f before %.2f", end, result.Duration+1)
	}
	// Every bar in the track keeps its downbeat on a multiple of four within 10 ms.
	for bar, want := range downbeats {
		if got := beatAt(tempo.Anchors, int64(bar*4)); math.Abs(got-want) > 0.01 {
			t.Fatalf("bar %d downbeat at %.4f, want %.4f", bar, got, want)
		}
	}
	if result.Key.Camelot != "8A" || result.Key.Index != 1 {
		t.Fatalf("key=%+v", result.Key)
	}
	if tempo.Confidence <= 0 || tempo.Confidence > 1 || result.Key.Confidence <= 0 || result.Key.Confidence > 1 {
		t.Fatalf("confidence tempo=%v key=%v", tempo.Confidence, result.Key.Confidence)
	}
}

func TestTempoChangesBecomeAnchorsOnTheNewTempo(t *testing.T) {
	samples, downbeats := synthesize(0, aMinorProgression, section{bpm: 122, bars: 48}, section{bpm: 128, bars: 48})
	result, err := Samples(context.Background(), samples, Options{Tempo: true})
	if err != nil || result.TempoErr != nil {
		t.Fatal(err, result.TempoErr)
	}
	anchors := result.Tempo.Anchors
	if len(anchors) != 3 {
		t.Fatalf("anchors=%+v", anchors)
	}
	for _, check := range []struct {
		bar  int
		bpm  float64
		from int
	}{{bar: 4, bpm: 122, from: 0}, {bar: 60, bpm: 128, from: 1}} {
		segment := anchors[check.from : check.from+2]
		bpm := float64(segment[1].Index-segment[0].Index) / (segment[1].Time - segment[0].Time) * 60
		if math.Abs(bpm-check.bpm) > 0.1 {
			t.Fatalf("segment %d bpm=%.3f, want %.0f", check.from, bpm, check.bpm)
		}
		if got := beatAt(anchors, int64(check.bar*4)); math.Abs(got-downbeats[check.bar]) > 0.02 {
			t.Fatalf("bar %d at %.3f, want %.3f", check.bar, got, downbeats[check.bar])
		}
	}
	if result.Key != nil {
		t.Fatal("key analysis ran without being requested")
	}
}

func TestTempoRangeFoldsIntoTheRequestedOctave(t *testing.T) {
	samples, _ := synthesize(0, aMinorProgression, section{bpm: 87, bars: 32})
	result, err := Samples(context.Background(), samples, Options{Tempo: true, MinBPM: 98})
	if err != nil || result.TempoErr != nil {
		t.Fatal(err, result.TempoErr)
	}
	if math.Abs(result.Tempo.BPM-174) > 0.05 {
		t.Fatalf("bpm=%.3f, want 174", result.Tempo.BPM)
	}
}

func TestSilenceAndShortAudioReportNoResult(t *testing.T) {
	silence := make([]float32, 40*SampleRate)
	result, err := Samples(context.Background(), silence, Options{Tempo: true, Key: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tempo != nil || !errors.Is(result.TempoErr, ErrNoBeat) || result.Key != nil || !errors.Is(result.KeyErr, ErrNoKey) {
		t.Fatalf("silence: %+v", result)
	}
	short, err := Samples(context.Background(), make([]float32, SampleRate/10), Options{Tempo: true, Key: true})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(short.TempoErr, ErrTooShort) || !errors.Is(short.KeyErr, ErrTooShort) {
		t.Fatalf("short: %+v", short)
	}
}

func TestOptionsAndCancellation(t *testing.T) {
	for _, o := range []Options{{}, {Tempo: true, MinBPM: 30}, {Key: true, MinBPM: math.NaN()}, {Tempo: true, MinBPM: 126}} {
		if _, err := Samples(context.Background(), nil, o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	samples, _ := synthesize(0, aMinorProgression, section{bpm: 120, bars: 16})
	if _, err := Samples(ctx, samples, Options{Tempo: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
