package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
)

func TestWatchRestoreAbsentPairDoesNotEnrollOnObservation(t *testing.T) {
	library := createDummyEngineLibrary(t)
	snapshot, err := CreateBackup(t.Context(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	state, err := queue.WatchAdd(t.Context(), intake.WatchAddOptions{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(t.Context(), library, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
		return RestoreBackup(ctx, snapshot.Path, library)
	})
	if err != nil {
		t.Fatal(err)
	}
	checkAbsent := func() {
		t.Helper()
		observed, err := intake.WatchList(t.Context(), library)
		if err != nil || !observed.Suspended || observed.Reason != "restored" || len(observed.Roots) != 0 || observed.Revision <= state.Revision {
			t.Fatalf("absent pair lost authorization fence: %+v %v", observed, err)
		}
		state = observed
		for _, name := range []string{companion.IntakeName, companion.WatchName} {
			if path, err := companion.Database(library, name); err != nil || path != "" {
				t.Fatalf("observation enrolled %s: %q %v", filepath.Base(name), path, err)
			}
		}
	}
	checkAbsent()
	undo, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := intake.WatchList(t.Context(), library)
	if err != nil || len(observed.Roots) != 1 || !observed.Suspended {
		t.Fatalf("Undo did not restore complete prior pair: %+v %v", observed, err)
	}
	state = observed
	if _, err := history.Undo(t.Context(), library, undo.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	checkAbsent()
}

func TestWatchChangesDoNotConflictWithOrdinaryCollectionUndo(t *testing.T) {
	library := createDummyEngineLibrary(t)
	queue, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	state, err := queue.WatchAdd(t.Context(), intake.WatchAddOptions{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	var original string
	if err := d.QueryRow("SELECT value FROM fixture").Scan(&original); err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(t.Context(), library, "collection.edit", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := d.ExecContext(ctx, "UPDATE fixture SET value='ordinary collection edit'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = queue.WatchSetEnabled(t.Context(), intake.WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal("operational config caused ordinary Undo conflict:", err)
	}
	observed, err := intake.WatchList(t.Context(), library)
	if err != nil || observed.Revision != state.Revision || observed.Suspended || len(observed.Roots) != 1 || observed.Roots[0].Enabled {
		t.Fatalf("ordinary Undo changed discovery authorization: %+v %v", observed, err)
	}
	d, err = db.Open(filepath.Join(library, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var restored string
	if err := d.QueryRow("SELECT value FROM fixture").Scan(&restored); err != nil || restored != original {
		t.Fatalf("ordinary Undo did not restore collection: %q %v", restored, err)
	}
}
