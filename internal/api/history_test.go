package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestAPIHistoryUndo(t *testing.T) {
	s, lib := testAPI(t)
	job := runAPIJob(t, s, "playlist.create", `{"name":"Reversible"}`)
	id, ok := job["history_id"].(string)
	if !ok || id == "" {
		t.Fatalf("history ID missing: %v", job)
	}
	runAPIJob(t, s, "library.history", `{}`)
	s.config.NoBackup = false
	preview := runAPIJob(t, s, "library.undo", fmt.Sprintf(`{"id":%q,"dry_run":true}`, id))
	if preview["history_id"] != nil {
		t.Fatalf("preview created history: %v", preview)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "backups")); !os.IsNotExist(err) {
		t.Fatalf("preview created backup: %v", err)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 {
		t.Fatalf("preview modified history: %v %v", entries, err)
	}
	undo := runAPIJob(t, s, "library.undo", fmt.Sprintf(`{"id":%q}`, id))
	if undo["history_id"] == nil || undo["history_id"] == id {
		t.Fatalf("undo missing own recovery ID: %v", undo)
	}
}
