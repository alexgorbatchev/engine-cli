package history

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSmartlistAmendmentUsesDeletionTimePaths(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
		VALUES ('c','Unrelated cycle C','Sets;','','d','opaque-c'),('d','Unrelated cycle D','Sets;','','c','opaque-d')`); err != nil {
		t.Fatal(err)
	}
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	recordSmartlistMutation(t, lib, database, "playlist.move", `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`)
	deleted := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
	// A later display-name change is not a new native identity or linkage.
	// The amended delete must retain the name from its own historical images.
	recordSmartlistMutation(t, lib, database, "track.update", `UPDATE Smartlist SET title='Later saved name' WHERE listUuid='saved'`)
	evidence, err := readSmartlistEvidence(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planSmartlistLinks(ctx, evidence, orders)
	if err != nil {
		t.Fatal(err)
	}
	amendments, err := planSmartlistDeletionAmendments(ctx, lib, evidence, orders, plan)
	if err != nil || (amendments == nil || len(amendments.deletions) != 1) {
		t.Fatalf("amendments=%+v error=%v", amendments, err)
	}
	amendment := amendments.deletions[0]
	if amendment.entryID != deleted.ID || len(amendment.before.patches) != 1 || len(amendment.after.patches) != 1 {
		t.Fatalf("wrong original deletion scope: %+v", amendment)
	}
	patch := amendment.before.patches[0]
	if patch.before.UUID != "saved" || patch.after == nil || patch.after.ParentPath != "Sets;Renamed;" {
		t.Fatalf("Undo Before borrowed wrong path generation: %+v", patch)
	}
	for _, side := range []struct {
		name string
		plan smartlistHistoricalAmendment
	}{{"before", amendment.before}, {"after", amendment.after}} {
		destination := filepath.Join(t.TempDir(), side.name+".db")
		if _, err := stageHistoricalSmartlistImage(ctx, destination, side.plan); err != nil {
			t.Fatal(err)
		}
		inventory, _, err := readSmartlistVersion(ctx, destination, "")
		if err != nil {
			t.Fatal(err)
		}
		row, exists := smartlistSavedRow(inventory, "saved")
		if side.name == "before" && (!exists || row.ParentPath != "Sets;Renamed;") || side.name == "after" && exists {
			t.Fatalf("wrong historical %s saved row: %+v exists=%v", side.name, row, exists)
		}
		if side.name == "before" && row.Name == "Later saved name" {
			t.Fatalf("historical Before acquired a future saved name: %+v", row)
		}
		if len(inventory.Issues) != 2 {
			t.Fatalf("unrelated historical cycle changed: %+v", inventory.Issues)
		}
		if actual, err := fingerprint(ctx, side.plan.image.Path); err != nil || actual != side.plan.image.Hash {
			t.Fatalf("original historical %s changed: %q %v", side.name, actual, err)
		}
	}
}

func TestSmartlistAmendmentDoesNotBorrowUndoAcrossSavedIdentityDiscontinuity(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"observed UUID absence", `DELETE FROM Smartlist WHERE listUuid='saved'`, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES ('saved','Saved','House;','','','opaque')`},
		{"observed different linkage", `UPDATE Smartlist SET parentPlaylistPath='Other;' WHERE listUuid='saved'`, `UPDATE Smartlist SET parentPlaylistPath='House;' WHERE listUuid='saved'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			deleted := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
			recordSmartlistMutation(t, lib, database, "track.update", tc.first)
			recordSmartlistMutation(t, lib, database, "track.update", tc.second)
			evidence, err := readSmartlistEvidence(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistLinks(ctx, evidence, orders)
			if err != nil || len(plan.deletions[deleted.ID]) != 1 {
				t.Fatalf("missing native parent deletion candidate: %+v %v", plan, err)
			}
			amendments, err := planSmartlistDeletionAmendments(ctx, lib, evidence, orders, plan)
			if err != nil || len(amendments.deletions) != 0 || len(amendments.withoutUndo) != 1 || amendments.withoutUndo[0].UUID != "saved" {
				t.Fatalf("saved identity borrowed obsolete Undo: %+v %v", amendments, err)
			}
		})
	}
}
