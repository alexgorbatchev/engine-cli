package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistUnprovableHistoricalOrderDoesNotBorrowDeleteUndo(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := database.Exec(`UPDATE Smartlist SET nextListUuid='missing' WHERE listUuid='saved'`); err != nil {
		t.Fatal(err)
	}
	deleted := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(dir, deleted.ID+".json")
	entryBytes, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || review.Removed != 1 || review.AmendedDeletes != 0 {
		t.Fatalf("unprovable historical order borrowed deletion: %+v %v", review, err)
	}
	result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken)
	if err != nil || result.Removed != 1 || result.AmendedDeletes != 0 || result.UndoID != "" {
		t.Fatalf("unprovable removal borrowed Undo: %+v %v", result, err)
	}
	current, _, err := readSmartlistVersion(ctx, filepath.Join(lib, "Database2", "m.db"), "")
	if err != nil || len(current.Saved) != 0 || len(current.Issues) != 0 {
		t.Fatalf("broken row retained: %+v %v", current, err)
	}
	after, err := os.ReadFile(entryPath)
	if err != nil || string(after) != string(entryBytes) {
		t.Fatalf("original delete entry changed: %v", err)
	}
	for _, side := range []*snapshot{deleted.Before, deleted.After} {
		for _, image := range side.Databases {
			if actual, err := fingerprint(ctx, image.Path); err != nil || actual != image.Hash {
				t.Fatalf("original delete image changed: %s %v", image.Path, err)
			}
		}
	}
}

func TestSmartlistAmendmentRejectsChangedDeletionAfterObservation(t *testing.T) {
	for _, kind := range []string{"missing entry", "changed operation", "missing before"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
			evidence, err := readSmartlistEvidence(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
			if err != nil {
				t.Fatal(err)
			}
			links, err := planSmartlistLinks(ctx, evidence, orders)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing entry":
				err = os.Remove(filepath.Join(dir, entry.ID+".json"))
			case "changed operation":
				entry.Operation = "track.update"
				err = writeEntry(dir, entry)
			case "missing before":
				entry.Before = nil
				err = writeEntry(dir, entry)
			}
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(lib, "Database2", "m.db")
			before, err := fingerprint(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := planSmartlistDeletionAmendments(ctx, lib, evidence, orders, links); got != nil || err == nil {
				t.Fatalf("changed deletion granted amendment: %+v %v", got, err)
			}
			if after, err := fingerprint(ctx, target); err != nil || after != before {
				t.Fatalf("amendment refusal changed live source: %s %v", after, err)
			}
		})
	}
}
