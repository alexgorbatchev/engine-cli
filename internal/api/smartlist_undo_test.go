package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIUndoBacksUpOnlyValidatedApplication(t *testing.T) {
	s, lib := testAPI(t)
	job := runAPIJob(t, s, "playlist.create", `{"name":"Reversible"}`)
	id := job["history_id"].(string)
	s.config.NoBackup = false
	for _, args := range []string{`{"id":"missing"}`, fmt.Sprintf(`{"id":%q,"dry_run":true}`, id)} {
		_, _, err := s.RunOperation(t.Context(), "library.undo", json.RawMessage(args))
		if args == `{"id":"missing"}` && err == nil {
			t.Fatal("missing Undo target succeeded")
		}
		if backups, readErr := backup.ListBackups(lib, ""); readErr != nil || len(backups) != 0 {
			t.Fatalf("refused/preview Undo created backups: %+v %v", backups, readErr)
		}
	}
	if _, _, err := s.RunOperation(t.Context(), "library.undo", json.RawMessage(fmt.Sprintf(`{"id":%q}`, id))); err != nil {
		t.Fatal(err)
	}
	if backups, err := backup.ListBackups(lib, ""); err != nil || len(backups) != 1 {
		t.Fatalf("application must create exactly one protected backup: %+v %v", backups, err)
	}
}

func TestAPIUndoPendingRefusalPrecedesBackup(t *testing.T) {
	s, lib := testAPI(t)
	job := runAPIJob(t, s, "playlist.create", `{"name":"Reversible"}`)
	s.config.NoBackup = false
	dir := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte("interrupted publication intent"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.RunOperation(t.Context(), "library.undo", json.RawMessage(fmt.Sprintf(`{"id":%q,"force":true}`, job["history_id"])))
	if progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("pending Undo returned %v", err)
	}
	if backups, err := backup.ListBackups(lib, ""); err != nil || len(backups) != 0 {
		t.Fatalf("pending Undo created backup: %+v %v", backups, err)
	}
}
