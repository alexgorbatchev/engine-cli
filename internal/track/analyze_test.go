package track

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/analysis"
	"github.com/alexgorbatchev/engine-cli/internal/beatgrid"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

// fakeDecoder installs ffmpeg and ffprobe stand-ins: ffmpeg streams the PCM sidecar written next to the requested
// file and fails for files without one, and ffprobe reports a 44.1 kHz stream.
func fakeDecoder(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	scripts := map[string]string{
		"ffmpeg":  "#!/bin/sh\nwhile [ \"$1\" != \"-i\" ]; do shift; done\n[ -f \"${2#file:}.pcm\" ] || { echo missing >&2; exit 1; }\ncat \"${2#file:}.pcm\"\n",
		"ffprobe": "#!/bin/sh\necho '{\"streams\":[{\"sample_rate\":\"44100\"}]}'\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// music renders 120 BPM kicks under an A minor progression that changes chords on every downbeat, starting at 0.5 s.
func music(seconds float64) []float32 {
	samples := make([]float32, int(seconds*analysis.SampleRate))
	chords := [][]float64{{220, 261.63, 329.63}, {146.83, 174.61, 220}, {164.81, 207.65, 246.94}, {220, 261.63, 329.63}}
	for i := range samples {
		t := float64(i) / analysis.SampleRate
		if t < 0.5 {
			continue
		}
		beat := (t - 0.5) / 0.5
		since := (beat - math.Floor(beat)) * 0.5
		v := 0.6 * math.Exp(-since*18) * math.Sin(2*math.Pi*(50+90*math.Exp(-since*30))*since)
		for _, f := range chords[int(beat/4)%len(chords)] {
			v += 0.03 * math.Sin(2*math.Pi*f*t)
		}
		samples[i] = float32(v)
	}
	return samples
}

func writeAudio(t *testing.T, path string, samples []float32) {
	t.Helper()
	raw := make([]byte, 4*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(s))
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".pcm", raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// analysisLibrary creates a native library whose tracks 1 (music) and 2 (silence) have audio files.
func analysisLibrary(t *testing.T) (*sql.DB, string) {
	t.Helper()
	fakeDecoder(t)
	root := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := os.MkdirAll(filepath.Join(root, "Music"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAudio(t, filepath.Join(root, "Music", "song.mp3"), music(70))
	writeAudio(t, filepath.Join(root, "Music", "silence.mp3"), make([]float32, 40*analysis.SampleRate))
	if _, err := d.Exec("INSERT INTO Track(id, path, filename, length, bpm, bpmAnalyzed, key) VALUES(1, 'Music/song.mp3', 'song.mp3', 70, 90, 90.5, 4), (2, 'Music/silence.mp3', 'silence.mp3', 40, NULL, NULL, NULL), (3, 'Music/gone.mp3', 'gone.mp3', 10, NULL, NULL, NULL)"); err != nil {
		t.Fatal(err)
	}
	return d, root
}

func trackAnalysis(t *testing.T, d *sql.DB, id int64) (sql.NullInt64, sql.NullFloat64, sql.NullInt64) {
	t.Helper()
	var bpm sql.NullInt64
	var precise sql.NullFloat64
	var key sql.NullInt64
	if err := d.QueryRow("SELECT bpm, bpmAnalyzed, key FROM Track WHERE id=?", id).Scan(&bpm, &precise, &key); err != nil {
		t.Fatal(err)
	}
	return bpm, precise, key
}

func all() AnalysisSelection { return AnalysisSelection{BPM: true, Key: true, Grid: true} }

func TestAnalyzeWritesTempoKeyAndGridAndReportsFailuresPerTrack(t *testing.T) {
	d, root := analysisLibrary(t)
	guarded := 0
	report, err := Analyze(context.Background(), d, root, AnalysisRequest{Identifiers: []string{"1", "2", "Music/song.mp3", "3"}, AnalysisSelection: all()}, func(context.Context) error {
		guarded++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tracks) != 3 || guarded != 1 {
		t.Fatalf("tracks=%+v guard calls=%d", report.Tracks, guarded)
	}
	song := report.Tracks[0]
	if song.Title != "song" || report.Tracks[1].Title != "silence" {
		t.Fatalf("titles=%q %q", song.Title, report.Tracks[1].Title)
	}
	if song.TrackID != 1 || song.BPM.Status != AnalysisWritten || math.Abs(song.BPM.BPM-120) > 0.05 || song.Grid.Status != AnalysisWritten || song.Grid.TempoMarkers != 0 || song.Key.Status != AnalysisWritten || *song.Key.Key != 1 {
		t.Fatalf("song=%+v bpm=%+v key=%+v grid=%+v", song, song.BPM, song.Key, song.Grid)
	}
	bpm, precise, key := trackAnalysis(t, d, 1)
	if bpm.Int64 != 120 || math.Abs(precise.Float64-song.BPM.BPM) > 1e-9 || key.Int64 != 1 {
		t.Fatalf("stored bpm=%v precise=%v key=%v", bpm, precise, key)
	}
	grid, err := beatgrid.Load(context.Background(), d, 1)
	if err != nil || grid == nil {
		t.Fatal(grid, err)
	}
	if grid.SampleRate != 44100 || len(grid.Default) != 2 || grid.Default[0] != grid.Adjusted[0] || grid.Adjusted[0].Beat != -4 || grid.Source != engine.BeatGridSourceAnalysis {
		t.Fatalf("grid=%+v", grid)
	}
	// The first downbeat lands on the kick at 0.5 s.
	first := grid.Adjusted[0]
	if downbeat := (first.Sample + 4*(grid.Adjusted[1].Sample-first.Sample)/float64(first.BeatsUntilNext)) / 44100; math.Abs(downbeat-0.5) > 0.01 {
		t.Fatalf("downbeat at %v", downbeat)
	}
	if grid.SampleLimit != 71*44100 || grid.Adjusted[1].Sample < grid.SampleLimit {
		t.Fatalf("limit=%v end=%v", grid.SampleLimit, grid.Adjusted[1].Sample)
	}
	silence := report.Tracks[1]
	for _, o := range []*AnalysisOutcome{silence.BPM, silence.Grid} {
		if o.Status != AnalysisFailed || o.Error.Code != "no_beat" {
			t.Fatalf("silence=%+v", o)
		}
	}
	if silence.Key.Error.Code != "no_key" {
		t.Fatalf("silence key=%+v", silence.Key)
	}
	gone := report.Tracks[2]
	if gone.TrackID != 3 || gone.BPM.Error.Code != "audio_unavailable" || gone.Key.Error.Code != "audio_unavailable" {
		t.Fatalf("gone=%+v", gone.BPM)
	}
	if bpm, _, key := trackAnalysis(t, d, 2); bpm.Valid || key.Valid {
		t.Fatalf("failed analysis wrote bpm=%v key=%v", bpm, key)
	}
}

func TestAnalyzePreservesUnselectedAndExistingValues(t *testing.T) {
	d, root := analysisLibrary(t)
	ctx := context.Background()
	report, err := Analyze(ctx, d, root, AnalysisRequest{Identifiers: []string{"1"}, AnalysisSelection: AnalysisSelection{Key: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks[0].BPM != nil || report.Tracks[0].Grid != nil {
		t.Fatalf("unrequested outcomes: %+v", report.Tracks[0])
	}
	if bpm, precise, key := trackAnalysis(t, d, 1); bpm.Int64 != 90 || precise.Float64 != 90.5 || key.Int64 != 1 {
		t.Fatalf("bpm=%v precise=%v key=%v", bpm, precise, key)
	}
	if grid, _ := beatgrid.Load(ctx, d, 1); grid != nil {
		t.Fatal("key analysis wrote a grid")
	}
	// With every value present, missing-only analysis keeps them all and never decodes the audio.
	if err := os.Remove(filepath.Join(root, "Music", "song.mp3.pcm")); err != nil {
		t.Fatal(err)
	}
	analyzed, _ := engine.NewBeatData(48000, 71*48000, []engine.BeatAnchor{{Sample: 0, Beat: -4, BeatsUntilNext: 200}, {Sample: 200 * 24000, Beat: 196}})
	tx, _ := d.Begin()
	if err := beatgrid.Save(ctx, tx, 1, analyzed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	report, err = Analyze(ctx, d, root, AnalysisRequest{Identifiers: []string{"1"}, AnalysisSelection: AnalysisSelection{BPM: true, Key: true, Grid: true, MissingOnly: true}}, func(context.Context) error {
		t.Fatal("nothing to write, so the guard must not run")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []*AnalysisOutcome{report.Tracks[0].BPM, report.Tracks[0].Key, report.Tracks[0].Grid} {
		if o.Status != AnalysisKept {
			t.Fatalf("outcome=%+v", o)
		}
	}
}

func TestAnalyzeRespectsLocksAndSavedSampleRates(t *testing.T) {
	d, root := analysisLibrary(t)
	ctx := context.Background()
	// Engine's track data records the key; analysis updates it there as well as in Track.key.
	record := make([]byte, 68)
	binary.BigEndian.PutUint64(record, math.Float64bits(48000))
	binary.BigEndian.PutUint32(record[16:], 7)
	blob, _ := engine.QCompress(record)
	if _, err := d.Exec("UPDATE PerformanceData SET trackData=? WHERE trackId=1", blob); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE Track SET isBeatGridLocked=1 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(ctx, d, root, AnalysisRequest{Identifiers: []string{"1", "2"}, AnalysisSelection: AnalysisSelection{Key: true, Grid: true, BPMRange: "58-115"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks[1].Grid.Status != AnalysisLocked {
		t.Fatalf("locked=%+v", report.Tracks[1].Grid)
	}
	// The 58–115 range folds 120 BPM to 60 BPM; the grid uses Engine's recorded 48 kHz rate.
	grid, _ := beatgrid.Load(ctx, d, 1)
	if grid.SampleRate != 48000 || math.Abs(engine.BeatGridBPM(grid.SampleRate, grid.Adjusted)-60) > 0.05 {
		t.Fatalf("grid=%+v", grid)
	}
	// Writing a grid stores its tempo as the track's BPM, so the two never disagree.
	if bpm, precise, _ := trackAnalysis(t, d, 1); bpm.Int64 != 60 || math.Abs(precise.Float64-engine.BeatGridBPM(grid.SampleRate, grid.Adjusted)) > 1e-9 {
		t.Fatalf("grid-only analysis left BPM %v %v", bpm, precise)
	}
	var stored []byte
	if err := d.QueryRow("SELECT trackData FROM PerformanceData WHERE trackId=1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	data, err := engine.QUncompress(stored)
	if err != nil || binary.BigEndian.Uint32(data[16:]) != 1 || math.Float64frombits(binary.BigEndian.Uint64(data)) != 48000 {
		t.Fatalf("trackData=%x err=%v", data, err)
	}
}

func TestAnalyzeStopsBeforeWritingWhenTheGuardRefuses(t *testing.T) {
	d, root := analysisLibrary(t)
	refused := &progress.Error{Code: "engine_desktop_running", Cause: errors.New("running")}
	_, err := Analyze(context.Background(), d, root, AnalysisRequest{Identifiers: []string{"1"}, AnalysisSelection: all()}, func(context.Context) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("err=%v", err)
	}
	if bpm, _, key := trackAnalysis(t, d, 1); bpm.Int64 != 90 || key.Int64 != 4 {
		t.Fatal("refused analysis wrote values")
	}
}

func TestAnalyzeValidatesRequestsAndHonoursCancellation(t *testing.T) {
	d, root := analysisLibrary(t)
	for _, r := range []AnalysisRequest{
		{AnalysisSelection: all()},
		{Identifiers: []string{" "}, AnalysisSelection: all()},
		{Identifiers: []string{"1"}},
		{Identifiers: []string{"1"}, AnalysisSelection: AnalysisSelection{BPM: true, BPMRange: "1-2"}},
		{Identifiers: make([]string, MaxMetadataBatch+1), AnalysisSelection: all()},
	} {
		if _, err := Analyze(context.Background(), d, root, r, nil); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	var coded *progress.Error
	if _, err := Analyze(context.Background(), d, root, AnalysisRequest{Identifiers: []string{"99"}, AnalysisSelection: all()}, nil); !errors.As(err, &coded) || coded.Code != "not_found" {
		t.Fatalf("missing track err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Analyze(ctx, d, root, AnalysisRequest{Identifiers: []string{"1", "2"}, AnalysisSelection: all()}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestAnalyzeImportedReplacesTagTempoAndRepairsUnreadableData(t *testing.T) {
	d, root := analysisLibrary(t)
	ctx := context.Background()
	// An unreadable saved grid counts as missing, and unreadable track data is left for Engine to rebuild.
	if _, err := d.Exec("UPDATE PerformanceData SET beatData=X'0000000501', trackData=X'0000000501' WHERE trackId=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeImported(ctx, d, root, []int64{1}, "1-2", nil); err == nil {
		t.Fatal("accepted an unknown range")
	}
	report, err := AnalyzeImported(ctx, d, root, []int64{1}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	analyzed := report.Tracks[0]
	if analyzed.BPM.Status != AnalysisWritten || analyzed.Grid.Status != AnalysisWritten || analyzed.Key.Status != AnalysisWritten {
		t.Fatalf("analyzed=%+v %+v %+v", analyzed.BPM, analyzed.Grid, analyzed.Key)
	}
	if bpm, _, key := trackAnalysis(t, d, 1); bpm.Int64 != 120 || key.Int64 != 1 {
		t.Fatalf("bpm=%v key=%v", bpm, key)
	}
	var trackData []byte
	if err := d.QueryRow("SELECT trackData FROM PerformanceData WHERE trackId=1").Scan(&trackData); err != nil || string(trackData) != "\x00\x00\x00\x05\x01" {
		t.Fatalf("trackData=%x err=%v", trackData, err)
	}
}

func TestAnalysisErrorsUseStableCodes(t *testing.T) {
	for err, code := range map[error]string{
		analysis.ErrNoBeat:     "no_beat",
		analysis.ErrNoKey:      "no_key",
		analysis.ErrTooShort:   "audio_too_short",
		errors.New("decoding"): "analysis_failed",
	} {
		if got := analysisError(err); got.Code != code || got.Message == "" {
			t.Fatalf("%v: %+v", err, got)
		}
	}
}

func TestMissingOnlyGridAnalysisReplacesADisagreeingTagTempo(t *testing.T) {
	d, root := analysisLibrary(t)
	report, err := Analyze(context.Background(), d, root, AnalysisRequest{Identifiers: []string{"1"}, AnalysisSelection: AnalysisSelection{BPM: true, Grid: true, MissingOnly: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The saved 90.5 BPM came from a tag; the new grid is 120 BPM, so the stored BPM follows the grid.
	if o := report.Tracks[0].BPM; o.Status != AnalysisWritten || math.Abs(o.BPM-120) > 0.05 {
		t.Fatalf("bpm outcome=%+v", o)
	}
	if bpm, _, _ := trackAnalysis(t, d, 1); bpm.Int64 != 120 {
		t.Fatalf("bpm=%v", bpm)
	}
}

func TestRefusedWritesStillReportTracksAlreadyWritten(t *testing.T) {
	d, root := analysisLibrary(t)
	writeAudio(t, filepath.Join(root, "Music", "other.mp3"), music(70))
	if _, err := d.Exec("INSERT INTO Track(id, path, filename, length) VALUES(4, 'Music/other.mp3', 'other.mp3', 70)"); err != nil {
		t.Fatal(err)
	}
	refused := &progress.Error{Code: "engine_desktop_running", Cause: errors.New("running")}
	writes := 0
	report, err := Analyze(context.Background(), d, root, AnalysisRequest{Identifiers: []string{"1", "4"}, AnalysisSelection: AnalysisSelection{Key: true}}, func(context.Context) error {
		if writes++; writes > 1 {
			return refused
		}
		return nil
	})
	// Whichever track finished first was written before Desktop started; only it is reported.
	if !errors.Is(err, refused) || report == nil || len(report.Tracks) != 1 || report.Tracks[0].Key.Status != AnalysisWritten {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	var key sql.NullInt64
	if err := d.QueryRow("SELECT key FROM Track WHERE id=?", report.Tracks[0].TrackID).Scan(&key); err != nil || key.Int64 != 1 {
		t.Fatalf("written key=%v err=%v", key, err)
	}
}

func TestCancellationAfterAWriteKeepsTheWrittenTracksInTheReport(t *testing.T) {
	d, root := analysisLibrary(t)
	writeAudio(t, filepath.Join(root, "Music", "other.mp3"), music(70))
	if _, err := d.Exec("INSERT INTO Track(id, path, filename, length) VALUES(4, 'Music/other.mp3', 'other.mp3', 70)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	// Cancellation arrives after the first track is written, before the second.
	report, err := Analyze(ctx, d, root, AnalysisRequest{Identifiers: []string{"1", "4"}, AnalysisSelection: AnalysisSelection{Key: true}}, func(ctx context.Context) error {
		if writes++; writes == 2 {
			cancel()
		}
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || report == nil || len(report.Tracks) != 1 || report.Tracks[0].Key.Status != AnalysisWritten {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
