package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestIntakePreservationRefusesNonDatabase(t *testing.T) {
	for _, kind := range []string{"directory", "invalid database"} {
		t.Run(kind, func(t *testing.T) {
			lib, d, _ := fixture(t)
			path := filepath.Join(lib, "affected.db")
			if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("retained non-database evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			mutated := false
			_, err := Run(t.Context(), lib, "library.replace", ScopeCollection, nil, func(ctx context.Context) error {
				if err := PreserveDatabase(ctx, path); err != nil {
					return err
				}
				mutated = true
				_, err := d.Exec("UPDATE Track SET title='Changed'")
				return err
			})
			if err == nil || mutated {
				t.Fatalf("invalid preservation permitted mutation: %v, mutated=%v", err, mutated)
			}
			var title string
			if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Original" {
				t.Fatalf("collection changed: %q %v", title, err)
			}
			if kind == "invalid database" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "retained non-database evidence" {
					t.Fatalf("evidence changed: %q %v", data, err)
				}
			}
		})
	}
}

func TestIntakeReplacementRetainsBeforeWhenAfterIsNonRegular(t *testing.T) {
	lib, _, _ := fixture(t)
	path := intakeDatabaseFixture(t, lib)
	entry, err := Run(t.Context(), lib, "library.replace", ScopeCollection, nil, func(ctx context.Context) error {
		if err := PreserveDatabase(ctx, path); err != nil {
			return err
		}
		if err := removeDatabase(path); err != nil {
			return err
		}
		return os.Mkdir(path, 0700)
	})
	if err == nil || entry == nil || entry.After != nil {
		t.Fatalf("invalid After accepted: %+v %v", entry, err)
	}
	all, err := List(lib)
	if err != nil || len(all) != 1 || all[0].ID != entry.ID || all[0].After != nil {
		t.Fatalf("durable pending entry lost: %+v %v", all, err)
	}
	before := all[0].Before.Databases[path]
	if before.Path == "" {
		t.Fatal("original intake database was not retained")
	}
	assertIntakeReceipt(t, before.Path, "queued")
	mutated := false
	_, err = Run(t.Context(), lib, "track.edit", ScopeCollection, nil, func(context.Context) error { mutated = true; return nil })
	if progress.ErrorCode(err) != "history_recovery_required" || mutated {
		t.Fatalf("pending replacement did not exclude later mutation: %v %v", err, mutated)
	}
}

func TestIntakeCancelledRecorderRefusesImportIdentity(t *testing.T) {
	lib, _, _ := fixture(t)
	_, err := Run(t.Context(), lib, "intake.import", ScopeCollection, nil, func(ctx context.Context) error {
		if id, err := OperationID(ctx, lib, "intake.import", ScopeCollection); err != nil || id == "" {
			t.Fatalf("live recorder refused: %q %v", id, err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if id, err := OperationID(cancelled, lib, "intake.import", ScopeCollection); id != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled recorder authorized import: %q %v", id, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
