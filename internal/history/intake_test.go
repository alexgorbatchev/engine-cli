package history

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
)

func TestCollectionUndoRetainsIntakeJournal(t *testing.T) {
	lib, d, _ := fixture(t)
	path := intakeDatabaseFixture(t, lib)
	ctx := t.Context()
	queue, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { queue.Close() })
	e, err := Run(ctx, lib, "intake.import", ScopeCollection, nil, func(context.Context) error {
		if _, err := d.Exec(`UPDATE Track SET title='Imported' WHERE id=1`); err != nil {
			return err
		}
		_, err := queue.Exec(`UPDATE Receipt SET state='importing' WHERE id=1`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Exec(`UPDATE Receipt SET state='imported' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := d.QueryRow(`SELECT title FROM Track WHERE id=1`).Scan(&title); err != nil || title != "Original" {
		t.Fatalf("collection Undo: %q, %v", title, err)
	}
	assertIntakeReceipt(t, path, "imported")
}

func TestExplicitIntakeReplacementUndo(t *testing.T) {
	for _, mode := range []string{"replace", "remove", "create"} {
		t.Run(mode, func(t *testing.T) {
			lib, _, _ := fixture(t)
			path := intakeDatabaseFixture(t, lib)
			if mode == "create" {
				if err := removeDatabase(path); err != nil {
					t.Fatal(err)
				}
			}
			e, err := Run(t.Context(), lib, "library.replace", ScopeCollection, nil, func(ctx context.Context) error {
				if err := PreserveDatabase(ctx, path); err != nil {
					return err
				}
				if mode == "remove" {
					return removeDatabase(path)
				}
				q, err := db.Open(path)
				if err != nil {
					return err
				}
				_, execErr := q.Exec(`CREATE TABLE IF NOT EXISTS Receipt(id INTEGER PRIMARY KEY,state TEXT); INSERT OR REPLACE INTO Receipt VALUES(1,'replacement')`)
				if execErr == nil && mode == "create" {
					execErr = createIntakeControl(t.Context(), q)
				}
				closeErr := q.Close()
				if execErr != nil {
					return execErr
				}
				return closeErr
			})
			if err != nil {
				t.Fatal(err)
			}
			undo, err := Undo(t.Context(), lib, e.ID, UndoOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "create" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("created journal retained: %v", err)
				}
			} else {
				assertIntakeReceipt(t, path, "queued")
			}
			if _, err := Undo(t.Context(), lib, undo.ID, UndoOptions{}); err != nil {
				t.Fatalf("Undo of replacement Undo: %v", err)
			}
			if mode == "remove" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("removed journal restored by inverse: %v", err)
				}
			} else {
				assertIntakeReceipt(t, path, "replacement")
			}
		})
	}
}

func intakeDatabaseFixture(t *testing.T, lib string) string {
	t.Helper()
	path := filepath.Join(lib, ".engine-cli", companion.IntakeName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE Receipt(id INTEGER PRIMARY KEY,state TEXT); INSERT INTO Receipt VALUES(1,'queued')`); err != nil {
		t.Fatal(err)
	}
	if err := createIntakeControl(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func createIntakeControl(ctx context.Context, d *sql.DB) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := intakecontrol.Create(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func assertIntakeReceipt(t *testing.T, path, want string) {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var state string
	if err := d.QueryRow(`SELECT state FROM Receipt WHERE id=1`).Scan(&state); err != nil || state != want {
		t.Fatalf("receipt=%q, want %q, error=%v", state, want, err)
	}
}
