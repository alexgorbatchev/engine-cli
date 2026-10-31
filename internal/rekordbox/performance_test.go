package rekordbox

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

func TestPreparePerformanceMapsSupportedPositionsAndPreservesUnsupportedSource(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"><TEMPO Inizio="0.5" Bpm="120" Metro="4/4" Battito="3"/><POSITION_MARK Name="Memory" Type="0" Start="0" Num="-1"/><POSITION_MARK Name="Fraction" Type="0" Start="1.125" Num="7"/><POSITION_MARK Name="End loop" Type="4" Start="2.25" End="10" Num="0"/></TRACK></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := preparePerformance(src.xml.Collection.Tracks[0], src.library.Songs[0], 48000, 480000)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := engine.DecodeSavedPerformance(got.beat, got.cues, got.loops)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.HotCues) != 1 || saved.HotCues[0].Position != 8 || saved.HotCues[0].Offset != 1.125 || saved.HotCues[0].Name != "Fraction" || saved.HotCues[0].Color != "#FFFFFF" {
		t.Fatalf("source hot cue slot/position/name: %+v", saved)
	}
	if len(saved.Loops) != 1 || saved.Loops[0].Position != 1 || saved.Loops[0].Start != 2.25 || saved.Loops[0].End != 10 || saved.Loops[0].Color != "#FFFFFF" {
		t.Fatalf("source loop: %+v", saved)
	}
	grid, err := engine.DecodeBeatData(got.beat)
	if err != nil || !grid.IsSet || grid.Adjusted[0].Sample != 24000 || grid.Adjusted[0].Beat != 2 || grid.SampleLimit != 480000 {
		t.Fatalf("tempo/extent changed: %+v, %v", grid, err)
	}
	cues, err := engine.QUncompress(got.cues)
	if err != nil {
		t.Fatal(err)
	}
	// Main cue and its initial value remain absent; memory cues do not become it.
	if math.Float64frombits(binary.BigEndian.Uint64(cues[len(cues)-17:])) != -1 || math.Float64frombits(binary.BigEndian.Uint64(cues[len(cues)-8:])) != -1 {
		t.Fatal("memory cue became a native main cue")
	}
	if len(sourceWarnings(src)) != 1 || !unsupportedMarks(src.xml.Collection.Tracks[0]) {
		t.Fatal("unsupported memory mark lost its archive warning")
	}
}

func TestPreparePerformanceDoesNotInventMeterAndRejectsInvalidNativePositions(t *testing.T) {
	for _, meter := range []string{"3/4", ""} {
		t.Run("unsupported meter "+meter, func(t *testing.T) {
			tempos := []rbxml.Tempo{{Metro: meter}}
			got, err := preparePerformance(rbxml.Track{Tempo: &tempos}, lib.Song{Grid: []lib.Marker{{Bpm: 120}}}, 48000, 480000)
			if err != nil {
				t.Fatal(err)
			}
			grid, err := engine.DecodeBeatData(got.beat)
			if err != nil || grid.IsSet || len(grid.Adjusted) != 0 {
				t.Fatalf("unsupported meter became native 4/4: %+v, %v", grid, err)
			}
		})
	}
	for name, song := range map[string]lib.Song{
		"grid beyond end": {Grid: []lib.Marker{{StartPosition: 11, Bpm: 120}}},
		"cue at end":      {Cues: []lib.HotCue{{Position: 1, Offset: 10}}},
		"duplicate slot":  {Cues: []lib.HotCue{{Position: 1}, {Position: 1}}},
		"loop beyond end": {Loops: []lib.Loop{{Position: 1, Start: 1, End: 11}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := preparePerformance(rbxml.Track{}, song, 48000, 480000)
			if err == nil || got.beat != nil || got.cues != nil || got.loops != nil {
				t.Fatalf("invalid position produced partial performance: %+v, %v", got, err)
			}
		})
	}
	for _, frames := range []int64{0, -1, 1<<53 + 1} {
		if got, err := preparePerformance(rbxml.Track{}, lib.Song{}, 48000, frames); err == nil || got.beat != nil {
			t.Fatalf("invalid extent accepted: %d, %v", frames, err)
		}
	}
}

func TestPreparePerformancePreservesAllNativeSlotsWithoutImportingExtraSlots(t *testing.T) {
	var song lib.Song
	for slot := 1; slot <= nativeMarkerSlots+1; slot++ {
		song.Cues = append(song.Cues, lib.HotCue{Position: slot, Offset: float64(slot) / 4})
		song.Loops = append(song.Loops, lib.Loop{Position: slot, Start: float64(slot) / 4, End: float64(slot)/4 + 1})
	}
	got, err := preparePerformance(rbxml.Track{}, song, 48000, 480000)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := engine.DecodeSavedPerformance(got.beat, got.cues, got.loops)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.HotCues) != nativeMarkerSlots || len(saved.Loops) != nativeMarkerSlots {
		t.Fatalf("native slots lost or extra slots mapped: %+v", saved)
	}
	for i := range nativeMarkerSlots {
		want := i + 1
		if saved.HotCues[i].Position != want || saved.Loops[i].Position != want || saved.HotCues[i].Offset != float64(want)/4 || saved.Loops[i].Start != float64(want)/4 {
			t.Fatalf("slot or fractional position changed: %+v", saved)
		}
	}
	if song.Cues[0].Color != "" || song.Loops[0].Color != "" {
		t.Fatal("preparing performance mutated the parsed source")
	}
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if got, err := preparePerformance(rbxml.Track{}, song, rate, 480000); err == nil || got.beat != nil || got.cues != nil || got.loops != nil {
			t.Fatalf("invalid sample rate produced partial output: %+v, %v", got, err)
		}
	}
}
