package api

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/analysis"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// installFakeDecoder makes ffmpeg stream the PCM sidecar of the requested file, or FAKE_PCM for files without one,
// and ffprobe report a 44.1 kHz stream.
func installFakeDecoder(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	scripts := map[string]string{
		"ffmpeg":  "#!/bin/sh\nwhile [ \"$1\" != \"-i\" ]; do shift; done\nif [ -f \"$2.pcm\" ]; then cat \"$2.pcm\"; elif [ -n \"$FAKE_PCM\" ]; then cat \"$FAKE_PCM\"; else exit 1; fi\n",
		"ffprobe": "#!/bin/sh\necho '{\"streams\":[{\"sample_rate\":\"44100\"}],\"format\":{\"duration\":\"40\"}}'\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// beatPCM renders 40 seconds of 125 BPM kicks under a sustained A minor chord as raw float samples.
func beatPCM(t *testing.T, path string) {
	t.Helper()
	samples := make([]byte, 4*40*analysis.SampleRate)
	for i := range len(samples) / 4 {
		x := float64(i) / analysis.SampleRate
		since := math.Mod(x, 0.48)
		v := 0.6*math.Exp(-since*18)*math.Sin(2*math.Pi*(50+90*math.Exp(-since*30))*since) +
			0.03*(math.Sin(2*math.Pi*220*x)+math.Sin(2*math.Pi*261.63*x)+math.Sin(2*math.Pi*329.63*x))
		binary.LittleEndian.PutUint32(samples[4*i:], math.Float32bits(float32(v)))
	}
	if err := os.WriteFile(path, samples, 0o644); err != nil {
		t.Fatal(err)
	}
}

// nativeAPI serves a native-schema library with two analyzable tracks.
func nativeAPI(t *testing.T) (*Server, string, *sql.DB) {
	t.Helper()
	installFakeDecoder(t)
	lib := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
		beatPCM(t, filepath.Join(lib, name+".pcm"))
	}
	if _, err := d.Exec("INSERT INTO Track(id, path, filename, length, bpm, bpmAnalyzed, key) VALUES(1, 'one.mp3', 'one.mp3', 40, 90, 90, 4), (2, 'two.mp3', 'two.mp3', 40, 90, 90, 4)"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s, err := New(Config{LibraryDir: lib, NoBackup: true, CheckSystem: desktopClosed})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, lib, d
}

func desktopClosed(context.Context, []string) (*systemcheck.Report, error) {
	return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
}

func storedTempo(t *testing.T, d *sql.DB, id int) (int64, int64) {
	t.Helper()
	var bpm, key int64
	if err := d.QueryRow("SELECT bpm, key FROM Track WHERE id=?", id).Scan(&bpm, &key); err != nil {
		t.Fatal(err)
	}
	return bpm, key
}

func TestTrackAnalyzeWritesUndoableResults(t *testing.T) {
	s, lib, d := nativeAPI(t)
	job := runAPIJob(t, s, "track.analyze", `{"identifiers":["1"],"bpm":true,"key":true,"grid":true}`)
	result := job["result"].(map[string]any)["tracks"].([]any)[0].(map[string]any)
	if result["bpm"].(map[string]any)["status"] != "written" || result["grid"].(map[string]any)["status"] != "written" || result["key"].(map[string]any)["key"] != 1.0 {
		t.Fatalf("result=%+v", result)
	}
	if bpm, key := storedTempo(t, d, 1); bpm != 125 || key != 1 {
		t.Fatalf("bpm=%d key=%d", bpm, key)
	}
	grid := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "")
	if grid.Code != http.StatusOK || !strings.Contains(grid.Body.String(), `"sampleRate":44100`) {
		t.Fatalf("grid=%d %s", grid.Code, grid.Body)
	}
	if _, err := history.Undo(context.Background(), lib, job["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if bpm, key := storedTempo(t, d, 1); bpm != 90 || key != 4 {
		t.Fatalf("undo left bpm=%d key=%d", bpm, key)
	}
	if w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", ""); w.Body.String() != "{\"grid\":null}\n" {
		t.Fatalf("undo left grid %s", w.Body)
	}
}

func TestTrackAnalyzeRechecksDesktopBeforeEachTrack(t *testing.T) {
	s, lib, d := nativeAPI(t)
	var checks atomic.Int32
	s.config.CheckSystem = func(ctx context.Context, names []string) (*systemcheck.Report, error) {
		// The job's own check and the first track's write pass; Desktop then starts.
		if checks.Add(1) > 2 {
			return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
		}
		return desktopClosed(ctx, names)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.analyze", `{"identifiers":["1","2"],"bpm":true}`))
	if job["error"].(map[string]any)["code"] != "engine_desktop_running" {
		t.Fatalf("job=%+v", job)
	}
	first, _ := storedTempo(t, d, 1)
	second, _ := storedTempo(t, d, 2)
	if first+second != 125+90 {
		t.Fatalf("exactly one track should be written: %d %d", first, second)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the partial write must keep its undo record: %v %v", entries, err)
	}
}

func TestTrackAnalyzeValidatesParameters(t *testing.T) {
	s, _, _ := nativeAPI(t)
	for _, body := range []string{`{"identifiers":["1"]}`, `{"identifiers":["1"],"bpm":true,"bpm_range":"10-20"}`, `{"identifiers":[],"bpm":true}`} {
		if w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.analyze", body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}

func TestGridEditsPreviewAndSaveUndoably(t *testing.T) {
	s, lib, d := nativeAPI(t)
	if w := requestAPI(t, s, http.MethodPost, "/v1/tracks/1/beatgrid/edits", `{"edit":"shift","seconds":0.01}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "beatgrid_missing") {
		t.Fatalf("missing grid: %d %s", w.Code, w.Body)
	}
	runAPIJob(t, s, "track.analyze", `{"identifiers":["1"],"grid":true}`)
	saved := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "").Body.String()
	for body, code := range map[string]string{
		`not json`:                           "beatgrid_edit_invalid",
		`{"edit":"warp"}`:                    "beatgrid_edit_invalid",
		`{"edit":"tempo","bpm":400}`:         "beatgrid_tempo_range",
		`{"edit":"shift","seconds":1,"x":1}`: "beatgrid_edit_invalid",
	} {
		if w := requestAPI(t, s, http.MethodPost, "/v1/tracks/1/beatgrid/edits", body); w.Code < 400 || !strings.Contains(w.Body.String(), code) {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	preview := requestAPI(t, s, http.MethodPost, "/v1/tracks/1/beatgrid/edits", `{"edit":"tempo","bpm":126}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview=%d %s", preview.Code, preview.Body)
	}
	var edited struct {
		Grid struct {
			BPM     float64           `json:"bpm"`
			Anchors []json.RawMessage `json:"anchors"`
		} `json:"grid"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &edited); err != nil || math.Abs(edited.Grid.BPM-126) > 1e-6 {
		t.Fatalf("preview=%s err=%v", preview.Body, err)
	}
	// Previews never write; chained edits start from the supplied draft.
	if requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "").Body.String() != saved {
		t.Fatal("preview changed the saved grid")
	}
	anchors, _ := json.Marshal(edited.Grid.Anchors)
	chained := requestAPI(t, s, http.MethodPost, "/v1/tracks/1/beatgrid/edits", fmt.Sprintf(`{"anchors":%s,"edit":"scale","factor":0.5}`, anchors))
	if !strings.Contains(chained.Body.String(), `"bpm":63`) {
		t.Fatalf("chained=%s", chained.Body)
	}
	job := runAPIJob(t, s, "track.grid.update", fmt.Sprintf(`{"identifier":"1","anchors":%s}`, anchors))
	if bpm, _ := storedTempo(t, d, 1); bpm != 126 {
		t.Fatalf("saved bpm=%d", bpm)
	}
	reset := requestAPI(t, s, http.MethodPost, "/v1/tracks/1/beatgrid/edits", `{"edit":"reset"}`)
	if !strings.Contains(reset.Body.String(), `"bpm":125`) {
		t.Fatalf("reset=%s", reset.Body)
	}
	if _, err := history.Undo(context.Background(), lib, job["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "").Body.String() != saved {
		t.Fatal("undo did not restore the analyzed grid")
	}
	failed := awaitAPIJob(t, s, submitAPIJob(t, s, "track.grid.update", `{"identifier":"1","anchors":[{"sample":0,"beat":0,"beatsUntilNext":1},{"sample":10,"beat":1}]}`))
	if failed["error"].(map[string]any)["code"] != "beatgrid_tempo_range" {
		t.Fatalf("failed=%+v", failed)
	}
	missing := awaitAPIJob(t, s, submitAPIJob(t, s, "track.grid.update", `{"identifier":"99","anchors":[{"sample":0,"beat":0,"beatsUntilNext":1},{"sample":22050,"beat":1}]}`))
	if missing["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("missing=%+v", missing)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.grid.update", `{"identifier":"1","anchors":[]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty anchors: %d", w.Code)
	}
}

func TestImportsAnalyzeAddedTracks(t *testing.T) {
	s, lib, d := nativeAPI(t)
	source := filepath.Join(t.TempDir(), "new.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	beatPCM(t, source+".pcm")
	added := runAPIJob(t, s, "track.add", fmt.Sprintf(`{"paths":[%q],"analyze":true}`, source))
	item := added["result"].([]any)[0].(map[string]any)
	analyzed := item["analysis"].(map[string]any)
	if analyzed["grid"].(map[string]any)["status"] != "written" || analyzed["key"].(map[string]any)["status"] != "written" {
		t.Fatalf("added=%+v", item)
	}
	if bpm, key := storedTempo(t, d, int(item["track_id"].(float64))); bpm != 125 || key != 1 {
		t.Fatalf("bpm=%d key=%d", bpm, key)
	}
	plain := runAPIJob(t, s, "track.add", fmt.Sprintf(`{"paths":[%q]}`, filepath.Join(lib, "two.mp3")))
	if _, ok := plain["result"].([]any)[0].(map[string]any)["analysis"]; ok {
		t.Fatalf("analysis ran without being requested: %+v", plain)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.add", `{"paths":["x"],"bpm_range":"88-175"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("range without analysis: %d", w.Code)
	}

	t.Setenv("FAKE_PCM", source+".pcm")
	store := staging.New(lib)
	ctx := context.Background()
	staged, err := store.Create(ctx, staging.Item{Kind: "track", Title: "Staged", Artist: "Artist", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(ctx, staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged.AudioPath = staged.ID + "/audio.mp3"
	if err := store.Update(ctx, *staged); err != nil {
		t.Fatal(err)
	}
	imported := runAPIJob(t, s, "staging.import", fmt.Sprintf(`{"ids":[%q],"analyze":true,"bpm_range":"58-115"}`, staged.ID))
	entry := imported["result"].(map[string]any)["imported"].([]any)[0].(map[string]any)
	if bpm := entry["analysis"].(map[string]any)["bpm"].(map[string]any)["bpm"].(float64); math.Abs(bpm-62.5) > 0.05 {
		t.Fatalf("imported=%+v", entry)
	}
}
