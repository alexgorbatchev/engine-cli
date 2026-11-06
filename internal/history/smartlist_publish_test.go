package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistPublicationReconnectHasOwnExactUndo(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken)
	if err != nil || result == nil || !result.Applied || result.UndoID == "" || result.Reconnected != 1 {
		t.Fatalf("reconnect publication: %+v %v", result, err)
	}
	current, _, err := readSmartlistVersion(ctx, filepath.Join(lib, "Database2", "m.db"), "")
	if err != nil || len(current.Issues) != 0 || current.Saved[0].ParentPath != "Renamed;" {
		t.Fatalf("repair not live: %+v %v", current, err)
	}
	if _, err := Undo(ctx, lib, result.UndoID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	current, _, err = readSmartlistVersion(ctx, filepath.Join(lib, "Database2", "m.db"), "")
	if err != nil || current.Saved[0].ParentPath != "House;" || current.Playlists[0].Path != "Renamed;" {
		t.Fatalf("reconnect Undo changed ordinary rename: %+v %v", current, err)
	}
}

func TestSmartlistPublicationAmendsOriginalDeleteWithoutChangingOriginalImages(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	recordSmartlistMutation(t, lib, database, "playlist.move", `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`)
	deleted := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
	target := filepath.Join(lib, "Database2", "m.db")
	before, after := deleted.Before.Databases[target], deleted.After.Databases[target]
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken)
	if err != nil || result == nil || !result.Applied || result.UndoID != "" || result.AmendedDeletes != 1 {
		t.Fatalf("deletion repair: %+v %v", result, err)
	}
	for _, original := range []databaseState{before, after} {
		if hash, err := fingerprint(ctx, original.Path); err != nil || hash != original.Hash {
			t.Fatalf("original overwritten: %s %v", hash, err)
		}
	}
	if _, err := Undo(ctx, lib, deleted.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	current, _, err := readSmartlistVersion(ctx, target, "")
	if err != nil || len(current.Issues) != 0 || len(current.Saved) != 1 || current.Saved[0].ParentPath != "Sets;Renamed;" {
		t.Fatalf("single original delete Undo: %+v %v", current, err)
	}
}

func TestSmartlistPublicationUnknownRemovalNoUndoAndHealthyNoEnrollment(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	healthy, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplySmartlistRepair(ctx, lib, healthy.PlanToken)
	if err != nil || result.Applied {
		t.Fatalf("healthy repair wrote: %+v %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("healthy repair enrolled: %v", err)
	}
	if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;' WHERE listUuid='saved'`); err != nil {
		t.Fatal(err)
	}
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ApplySmartlistRepair(ctx, lib, healthy.PlanToken); got != nil || progress.ErrorCode(err) != "smartlist_repair_stale" {
		t.Fatalf("stale consent accepted: %+v %v", got, err)
	}
	result, err = ApplySmartlistRepair(ctx, lib, review.PlanToken)
	if err != nil || !result.Applied || result.UndoID != "" || result.Removed != 1 {
		t.Fatalf("unknown removal: %+v %v", result, err)
	}
	listed, err := List(lib)
	if err != nil || len(listed) != 0 {
		t.Fatalf("unknown removal manufactured Undo: %+v %v", listed, err)
	}
}

func TestSmartlistPublicationReconnectUndoNeverResurrectsUnknownRemoval(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('unknown','Unknown','Missing;','','','{}')`); err != nil {
		t.Fatal(err)
	}
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken)
	if err != nil || result == nil || result.Reconnected != 1 || result.Removed != 1 || result.UndoID == "" {
		t.Fatalf("mixed repair: %+v %v", result, err)
	}
	if _, err := Undo(ctx, lib, result.UndoID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	current, _, err := readSmartlistVersion(ctx, filepath.Join(lib, "Database2", "m.db"), "")
	if err != nil || len(current.Saved) != 1 || current.Saved[0].UUID != "saved" || current.Saved[0].ParentPath != "House;" {
		t.Fatalf("reconnect Undo resurrected unknown removal: %+v %v", current, err)
	}
}
