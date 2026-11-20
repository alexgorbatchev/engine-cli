package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectionOperationThatLeftTheCollectionRecordsOnlyFiles(t *testing.T) {
	ctx := t.Context()
	lib, d, _ := fixture(t)
	staged := filepath.Join(lib, "staged.mp3")
	entry, err := Run(ctx, lib, "track.fetch", ScopeCollection, nil, func(ctx context.Context) error {
		if err := Created(ctx, staged); err != nil {
			return err
		}
		if err := os.WriteFile(staged, []byte("download"), 0o600); err != nil {
			return err
		}
		LeaveCollection(ctx)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Scope != ScopeFiles || len(entry.Before.Databases) != 0 || len(entry.After.Databases) != 0 {
		t.Fatalf("scope %q, databases %d/%d", entry.Scope, len(entry.Before.Databases), len(entry.After.Databases))
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history", entry.ID, "before")); !os.IsNotExist(err) {
		t.Fatalf("kept database snapshots: %v", err)
	}
	// A later collection edit is not part of this entry, so undoing it removes only the download.
	if _, err := d.Exec("UPDATE Track SET title='Later edit'"); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("undo kept the download: %v", err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track LIMIT 1").Scan(&title); err != nil || title != "Later edit" {
		t.Fatalf("undo changed the collection: %q %v", title, err)
	}
}

func TestCollectionOperationThatChangedADatabaseKeepsCollectionScope(t *testing.T) {
	ctx := t.Context()
	lib, d, _ := fixture(t)
	for _, leaves := range []bool{true, false} {
		entry, err := Run(ctx, lib, "track.fetch", ScopeCollection, nil, func(ctx context.Context) error {
			if leaves {
				LeaveCollection(ctx)
			}
			_, err := d.Exec("UPDATE Track SET title='Changed'")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if entry.Scope != ScopeCollection || len(entry.Before.Databases) == 0 {
			t.Fatalf("leaves=%v: scope %q", leaves, entry.Scope)
		}
	}
	// Outside a history run there is nothing to record.
	LeaveCollection(context.Background())
}
