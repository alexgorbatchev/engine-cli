package api

import (
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestPlaylistListingReportsUnreadableTracks(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "playlist.list", `{}`))
	if job["state"] != "failed" || job["result"] != nil || job["history_id"] != nil {
		t.Fatalf("unreadable tracks reported success or changes: %v", job)
	}
	failure := job["error"].(map[string]any)
	progress := job["progress"].(map[string]any)
	if failure["code"] != "operation_failed" || progress["error_code"] != "operation_failed" {
		t.Fatalf("missing stable failure classification: %v", job)
	}
}
