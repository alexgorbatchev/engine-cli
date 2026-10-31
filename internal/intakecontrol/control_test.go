package intakecontrol

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func controlDatabase(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	tx, err := d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Create(t.Context(), tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Error(rollbackErr)
		}
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSuspensionStatePersistsAndRejectsDamagedControl(t *testing.T) {
	for _, reason := range []string{"", "manual", "restored", "history_unavailable"} {
		t.Run("state="+reason, func(t *testing.T) {
			d := controlDatabase(t)
			if err := Set(t.Context(), d, reason); err != nil {
				t.Fatal(err)
			}
			state, err := Read(t.Context(), d)
			if err != nil || state.Reason != reason || state.Paused != (reason != "") {
				t.Fatalf("persisted suspension: %+v, %v", state, err)
			}
			if err := Set(t.Context(), d, "unknown"); err == nil {
				t.Fatal("invalid suspension accepted")
			}
			retained, err := Read(t.Context(), d)
			if err != nil || retained != state {
				t.Fatalf("refusal changed prior suspension: %+v, %v", retained, err)
			}
		})
	}
	for _, damage := range []string{"missing", "unknown", "schema"} {
		t.Run(damage, func(t *testing.T) {
			d := controlDatabase(t)
			statements := map[string]string{
				"missing": "DELETE FROM QueueControl",
				"unknown": "PRAGMA ignore_check_constraints=ON; UPDATE QueueControl SET reason='unknown'",
				"schema":  "DROP TABLE QueueControl",
			}
			if _, err := d.ExecContext(t.Context(), statements[damage]); err != nil {
				t.Fatal(err)
			}
			if state, err := Read(t.Context(), d); err == nil || state.Paused {
				t.Fatalf("damaged control produced a usable state: %+v, %v", state, err)
			}
			if damage != "unknown" {
				if err := Set(t.Context(), d, "restored"); err == nil {
					t.Fatal("missing suspension storage silently repaired")
				}
			}
		})
	}
}

func TestRestoredRefusesUnsafeStorageWithoutEnrolling(t *testing.T) {
	for _, layout := range []string{"absent", "parent-file", "database-directory", "broken-schema"} {
		t.Run(layout, func(t *testing.T) {
			library := t.TempDir()
			parent := filepath.Join(library, ".engine-cli")
			if layout == "parent-file" {
				if err := os.WriteFile(parent, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if layout != "absent" {
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(parent, "intake.db")
				if layout == "database-directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("not SQLite"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := Restored(t.Context(), library)
			if (err == nil) != (layout == "absent") {
				t.Fatalf("restored %s: %v", layout, err)
			}
			if layout == "absent" {
				entries, err := os.ReadDir(library)
				if err != nil || len(entries) != 0 {
					t.Fatalf("absent queue enrolled during restore: %v, %v", entries, err)
				}
			}
		})
	}
}
