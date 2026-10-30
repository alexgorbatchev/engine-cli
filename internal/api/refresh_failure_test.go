package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestRefreshFailureRetainsPartialResultAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	s.config.NoBackup = false
	s.config.CheckSystem = desktopClosed
	// Missing ffprobe is an expected unreadable-tags outcome, independent of host tools.
	t.Setenv("PATH", t.TempDir())
	for _, name := range []string{"song.mp3", "second.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("synthetic audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	execAPIFixture(t, lib, "UPDATE Track SET fileBytes=0 WHERE id=1; INSERT INTO Track(id,path,fileBytes) VALUES(2,'second.mp3',0); CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN NEW.id=2 BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END")
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "library.refresh", `{}`))
	if job["state"] != "failed" || job["error"].(map[string]any)["code"] != "library_refresh_write_failed" || job["progress"].(map[string]any)["error_code"] != "library_refresh_write_failed" {
		t.Fatalf("refresh failure missing: %v", job)
	}
	result, ok := job["result"].(map[string]any)
	if !ok || result["updatedCount"] != float64(1) || result["totalDbTracks"] != float64(2) {
		t.Fatalf("partial result missing: %v", job)
	}
	id, ok := job["history_id"].(string)
	if !ok || id == "" {
		t.Fatalf("partial history missing: %v", job)
	}
	assertHistoryScope(t, lib, id, history.ScopeCollection)
	snapshots, err := backup.ListBackups(lib, "")
	if err != nil || len(snapshots) == 0 {
		t.Fatalf("safety snapshot missing: %v %v", snapshots, err)
	}
	runAPIJob(t, s, "library.undo", fmt.Sprintf(`{"id":%q}`, id))
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, id := range []int64{1, 2} {
		track, err := db.GetTrackByID(t.Context(), d, id)
		if err != nil || track.FileBytes.Int64 != 0 {
			t.Fatalf("partial update not undone: track=%+v error=%v", track, err)
		}
	}
}

func TestRefreshFreshDesktopGuardPrecedesRecoveryWrites(t *testing.T) {
	s, lib := testAPI(t)
	s.config.NoBackup = false
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(checks > 1)}}}, nil
	}
	// A previous safe observation cannot authorize a later collection mutation.
	runAPIJob(t, s, "system.check", `{}`)
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "library.refresh", `{}`))
	if checks != 2 || job["state"] != "failed" || job["error"].(map[string]any)["code"] != "engine_desktop_running" || job["history_id"] != nil || job["result"] != nil {
		t.Fatalf("fresh guard failed: checks=%d job=%v", checks, job)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("blocked refresh created recovery data: %v", err)
	}
}
