package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/migrate"
	"github.com/dj/engine-cli/internal/progress"
	"github.com/dj/engine-cli/internal/systemcheck"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
)

func TestMigrationCatalog(t *testing.T) {
	s, _ := testAPI(t)
	w := requestAPI(t, s, http.MethodGet, "/v1/operations", "")
	if !strings.Contains(w.Body.String(), `"name":"migrate.export"`) {
		t.Fatalf("missing migration operation: %s", w.Body.String())
	}
}

func migrationSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "Engine Library")
	if err := engine.Export(lib.Library{DatabaseUUID: "api", Songs: []lib.Song{{SongID: 1, Path: "../song.mp3", Title: "Test", Filetype: "mp3"}}}, source, engine.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestMigrationAPIExportAndResume(t *testing.T) {
	s, _ := testAPI(t)
	s.config.LibraryDir = migrationSource(t)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return nil, errors.New("read-only export must not check for a library mutation")
	}
	target := t.TempDir()
	body, err := json.Marshal(map[string]string{"target": target})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"copied", "existing"} {
		job := runAPIJob(t, s, "migrate.export", string(body))
		r := job["result"].(map[string]any)
		if r[field] != float64(1) || job["history_id"] != nil {
			t.Fatalf("result=%+v", job)
		}
		p := job["progress"].(map[string]any)
		if p["percent"] != float64(100) || p["type"] != "complete" {
			t.Fatalf("progress=%v", p)
		}
	}
	if _, err := os.Stat(filepath.Join(s.config.LibraryDir, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatal("export wrote source history")
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/operations/migrate.export", `{"unknown":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("validation=%d", w.Code)
	}
	source := migrationSource(t)
	body, err = json.Marshal(map[string]string{"source": source, "target": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "migrate.export", string(body))
}

func TestMigrationAPIFailureAndQueuedCancellation(t *testing.T) {
	s, _ := testAPI(t)
	target := filepath.Join(t.TempDir(), "target")
	body, err := json.Marshal(map[string]string{"source": filepath.Join(t.TempDir(), "missing"), "target": target})
	if err != nil {
		t.Fatal(err)
	}
	id := submitAPIJob(t, s, "migrate.export", string(body))
	if job := awaitAPIJob(t, s, id); job["state"] != "failed" || job["error"] == nil {
		t.Fatalf("missing failure=%+v", job)
	}
	s.gate <- struct{}{}
	body, err = json.Marshal(migrate.Options{Source: migrationSource(t), Target: target})
	if err != nil {
		t.Fatal(err)
	}
	id = submitAPIJob(t, s, "migrate.export", string(body))
	if w := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", ""); w.Code != http.StatusAccepted {
		t.Fatalf("cancel=%d", w.Code)
	}
	<-s.gate
	if job := awaitAPIJob(t, s, id); job["state"] != "cancelled" {
		t.Fatalf("cancelled=%+v", job)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("cancelled job created output")
	}
}

func TestMigrationSSEStreamsIntermediateProgress(t *testing.T) {
	s, _ := testAPI(t)
	body, err := json.Marshal(migrate.Options{Source: migrationSource(t), Target: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := submitAPIJob(t, s, "migrate.export", string(body))
	if job := awaitAPIJob(t, s, id); job["state"] != "succeeded" {
		t.Fatalf("job=%+v", job)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id+"/events", "")
	if w.Code != http.StatusOK {
		t.Fatalf("SSE=%d %s", w.Code, w.Body.String())
	}
	var previous float64
	var sequence uint64
	phases := make(map[string]bool)
	var completed, sawBytes bool
	for _, line := range strings.Split(w.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event progress.Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatal(err)
		}
		if completed || event.JobID != id || event.Sequence <= sequence || event.Percent < previous || (event.Percent == 100 && event.Type != "complete") {
			t.Fatalf("invalid SSE event=%+v", event)
		}
		previous, sequence = event.Percent, event.Sequence
		phases[event.Phase] = true
		if len(event.Metadata) > 0 {
			var copy struct {
				Source string `json:"source"`
				Target string `json:"target"`
				Copied int64  `json:"copied_bytes"`
				Total  int64  `json:"total_bytes"`
			}
			if err := json.Unmarshal(event.Metadata, &copy); err != nil {
				t.Fatal(err)
			}
			sawBytes = copy.Copied == 5 && copy.Total == 5 && filepath.IsAbs(copy.Source) && filepath.IsAbs(copy.Target)
		}
		completed = event.Type == "complete"
	}
	if !completed || previous != 100 || !sawBytes || !phases["migrate.read"] || !phases["migrate.copy"] || !phases["migrate.xml"] {
		t.Fatalf("incomplete SSE: %s", w.Body.String())
	}
}
