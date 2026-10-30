package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/performance"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/nateranda/djtools/engine"
)

func cueLibrary(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	library := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), library); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec("INSERT INTO Track(id,path,title) VALUES(1,'offline.wav','Cue fixture')"); err != nil {
		t.Fatal(err)
	}
	beat, err := engine.QCompress(gridPayload())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE PerformanceData SET beatData=? WHERE trackId=1", beat); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{LibraryDir: library, CheckSystem: desktopClosed})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, d, library
}

func inspectedCue(t *testing.T, s *Server) performance.Saved {
	t.Helper()
	response := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/performance", "")
	if response.Code != http.StatusOK {
		t.Fatalf("inspect=%d %s", response.Code, response.Body)
	}
	var result performance.Saved
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func cueSetBody(t *testing.T, revision string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"identifier": "1", "slot": 8, "position": 1.23456789, "name": "Outro", "color": "#12ABEF", "revision": revision})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCueAPISetClearAndUndoPreservePerformance(t *testing.T) {
	s, d, library := cueLibrary(t)
	var originalBeat []byte
	if err := d.QueryRow("SELECT beatData FROM PerformanceData WHERE trackId=1").Scan(&originalBeat); err != nil {
		t.Fatal(err)
	}
	before := inspectedCue(t, s)
	job := runAPIJob(t, s, "track.cue.set", cueSetBody(t, before.Revision))
	result := job["result"].(map[string]any)
	saved := inspectedCue(t, s)
	if result["track_id"] != 1.0 || result["slot"] != 8.0 || result["changed"] != true || result["revision"] != saved.Revision || len(saved.Cues) != 1 || saved.Cues[0].Name != "Outro" || saved.Cues[0].Color != "#12ABEF" {
		t.Fatalf("job=%+v saved=%+v", job, saved)
	}
	clear, err := json.Marshal(map[string]any{"identifier": "1", "slot": 8, "revision": saved.Revision})
	if err != nil {
		t.Fatal(err)
	}
	cleared := runAPIJob(t, s, "track.cue.clear", string(clear))
	if current := inspectedCue(t, s); len(current.Cues) != 0 {
		t.Fatalf("clear left cues: %+v", current)
	}
	if _, err := history.Undo(context.Background(), library, cleared["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if restored := inspectedCue(t, s); len(restored.Cues) != 1 || restored.Cues[0] != saved.Cues[0] {
		t.Fatalf("undo did not restore cue: %+v", restored)
	}
	if _, err := history.Undo(context.Background(), library, job["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if restored := inspectedCue(t, s); len(restored.Cues) != 0 {
		t.Fatalf("undo left cue: %+v", restored)
	}
	var beat []byte
	if err := d.QueryRow("SELECT beatData FROM PerformanceData WHERE trackId=1").Scan(&beat); err != nil || !bytes.Equal(beat, originalBeat) {
		t.Fatalf("cue edits changed saved grid: %v", err)
	}
}

func TestCuePreflightRejectsBeforeSafetyArtifacts(t *testing.T) {
	for _, reason := range []string{"stale", "invalid", "desktop"} {
		t.Run(reason, func(t *testing.T) {
			s, _, library := cueLibrary(t)
			revision := inspectedCue(t, s).Revision
			body := cueSetBody(t, revision)
			want := "cue_revision_stale"
			switch reason {
			case "stale":
				body = cueSetBody(t, "outdated")
			case "invalid":
				var values map[string]any
				if err := json.Unmarshal([]byte(body), &values); err != nil {
					t.Fatal(err)
				}
				values["position"] = 1000
				data, err := json.Marshal(values)
				if err != nil {
					t.Fatal(err)
				}
				body, want = string(data), "cue_invalid"
			case "desktop":
				want = "engine_desktop_running"
				s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
					return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
				}
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.cue.set", body))
			if job["state"] != "failed" || job["error"].(map[string]any)["code"] != want {
				t.Fatalf("job=%+v", job)
			}
			for _, folder := range []string{"backups", "history"} {
				entries, err := os.ReadDir(filepath.Join(library, ".engine-cli", folder))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("rejected cue created %s artifacts", folder)
				}
			}
			if saved := inspectedCue(t, s); saved.Revision != revision || len(saved.Cues) != 0 {
				t.Fatalf("rejected cue wrote data: %+v", saved)
			}
		})
	}
}
