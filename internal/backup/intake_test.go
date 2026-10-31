package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
)

func TestIntakeSnapshotRestoreAndUndo(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent queue", true: "enrolled queue"}[present], func(t *testing.T) {
			lib := createDummyEngineLibrary(t)
			source := filepath.Join(t.TempDir(), "incoming.wav")
			if err := os.WriteFile(source, []byte("retained source"), 0600); err != nil {
				t.Fatal(err)
			}
			var queue *intake.Store
			var item *intake.Item
			var err error
			if present {
				queue, err = intake.Open(t.Context(), lib)
				if err != nil {
					t.Fatal(err)
				}
				item, err = queue.Enqueue(t.Context(), source)
				if err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := CreateBackup(t.Context(), lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				queue, err = intake.Open(t.Context(), lib)
				if err != nil {
					t.Fatal(err)
				}
				item, err = queue.Enqueue(t.Context(), source)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := queue.Cancel(t.Context(), item.ID); err != nil {
				t.Fatal(err)
			}
			// A replacement owner must stop/join processing and close the queue
			// before replacing its database. This test owns no background worker.
			if err := queue.Close(); err != nil {
				t.Fatal(err)
			}
			entry, err := history.Run(t.Context(), lib, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
				return RestoreBackup(ctx, snapshot.Path, lib)
			})
			if err != nil || entry == nil {
				t.Fatalf("restore: %+v, %v", entry, err)
			}
			if present {
				assertIntakeStatus(t, lib, item.ID, "queued")
				assertRestoredIntakePaused(t, lib)
			} else if path, err := companion.Database(lib, companion.IntakeName); err != nil || path != "" {
				t.Fatalf("absent queue restored as present: %q, %v", path, err)
			}
			if _, err := history.Undo(t.Context(), lib, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			assertIntakeStatus(t, lib, item.ID, "cancelled")
			assertRestoredIntakePaused(t, lib)
			if data, err := os.ReadFile(source); err != nil || string(data) != "retained source" {
				t.Fatalf("restore modified source: %q, %v", data, err)
			}
		})
	}
}

func assertRestoredIntakePaused(t *testing.T, library string) {
	t.Helper()
	queue, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	state, err := queue.Control(t.Context())
	if err != nil || !state.Paused || state.Reason != "restored" {
		t.Fatalf("replacement did not retain suspension: %+v, %v", state, err)
	}
	item, err := queue.PrepareNext(t.Context(), 0)
	if err != nil || item != nil {
		t.Fatalf("restored queue resumed machine-specific sources without review: %+v, %v", item, err)
	}
}

func TestInvalidIntakeSnapshotRefusesBeforeReplacement(t *testing.T) {
	for _, damage := range []string{"missing receipt table", "orphan receipt", "corrupt database", "missing importing receipt", "missing imported receipt", "missing cancelled receipt", "mismatched receipt"} {
		t.Run(damage, func(t *testing.T) {
			lib := createDummyEngineLibrary(t)
			source := filepath.Join(t.TempDir(), "source.wav")
			if err := os.WriteFile(source, []byte("preserved source"), 0600); err != nil {
				t.Fatal(err)
			}
			queue, err := intake.Open(t.Context(), lib)
			if err != nil {
				t.Fatal(err)
			}
			item, err := queue.Enqueue(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := CreateBackup(t.Context(), lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := queue.Cancel(t.Context(), item.ID); err != nil {
				t.Fatal(err)
			}
			if err := queue.Close(); err != nil {
				t.Fatal(err)
			}
			engine, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if _, err := engine.Exec(`UPDATE fixture SET value='live collection'`); err != nil {
				t.Fatal(err)
			}
			artwork := filepath.Join(lib, "Artwork", "sample_art.jpg")
			if err := os.WriteFile(artwork, []byte("live artwork"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(snapshot.Path, companionFolder, companion.IntakeName)
			if damage == "corrupt database" {
				if err := os.WriteFile(path, []byte("not SQLite"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				image, err := db.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				statement := `DROP TABLE ImportIntent`
				switch damage {
				case "orphan receipt":
					statement = `PRAGMA foreign_keys=OFF; INSERT INTO ImportIntent(item_id,history_id,plan_token,to_playlist) VALUES(999,'history','review','')`
				case "missing importing receipt":
					statement = `UPDATE Item SET status='importing',history_id='original-history'`
				case "missing imported receipt":
					statement = `UPDATE Item SET status='imported',history_id='original-history'`
				case "missing cancelled receipt":
					statement = `UPDATE Item SET status='cancelled',history_id='original-history'`
				case "mismatched receipt":
					statement = `UPDATE Item SET status='importing',history_id='original-history'; INSERT INTO ImportIntent(item_id,history_id,plan_token,to_playlist) SELECT id,'original-history','review','' FROM Item`
				}
				_, err = image.Exec(statement)
				if err == nil && damage == "mismatched receipt" {
					// A durable intent without a Track receipt is valid interrupted
					// work, not evidence of a completed import. Preserve it for recovery.
					if err := intake.ValidateDatabase(t.Context(), path); err != nil {
						t.Fatalf("valid pending intent refused: %v", err)
					}
					_, err = image.Exec(`UPDATE ImportIntent SET history_id='different-history'`)
				}
				closeErr := image.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("damage fixture: %v, close: %v", err, closeErr)
				}
			}
			if err := RestoreBackup(t.Context(), snapshot.Path, lib); err == nil {
				t.Fatal("invalid intake image accepted")
			}
			assertIntakeStatus(t, lib, item.ID, "cancelled")
			var value string
			if err := engine.QueryRow(`SELECT value FROM fixture`).Scan(&value); err != nil || value != "live collection" {
				t.Fatalf("refusal changed collection: %q, %v", value, err)
			}
			if data, err := os.ReadFile(artwork); err != nil || string(data) != "live artwork" {
				t.Fatalf("refusal changed artwork: %q, %v", data, err)
			}
		})
	}
}

func assertIntakeStatus(t *testing.T, lib string, id int64, status string) {
	t.Helper()
	s, err := intake.Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.List(t.Context())
	if closeErr := s.Close(); err != nil || closeErr != nil {
		t.Fatalf("read queue: %v, close: %v", err, closeErr)
	}
	if len(items) != 1 || items[0].ID != id || items[0].Status != status {
		t.Fatalf("queue state: %+v, want item %d %s", items, id, status)
	}
}
