package transfer

import (
	"reflect"
	"testing"
)

func TestSmartlistPlaylistMirrorSplicesRetainedChains(t *testing.T) {
	for _, policy := range []string{"error", "source", "destination"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Folder',0,0)`)
			execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Folder',0,0),(49,'First',48,50),(50,'Last',48,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('first','First saved','Folder;','Folder;First;',NULL,x'0102'),('last','Last saved','Folder;','Folder;Last;',NULL,x'0304')`)
			o := Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{12}}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}}, Conflict: policy, Mirror: true, NoBackup: true, DryRun: true}
			preview, err := Run(t.Context(), o)
			if err != nil || !preview.Applicable {
				t.Fatalf("preview %+v, %v", preview, err)
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			applied, err := Run(t.Context(), o)
			if err != nil || !reflect.DeepEqual(preview.Changes, applied.Changes) {
				t.Fatalf("apply %+v, %v", applied, err)
			}
			actual := savedRows(t, d)
			if len(actual) != 2 || actual["first"]["nextListUuid"] != "last" || actual["first"]["nextPlaylistPath"] != "" || actual["last"]["nextPlaylistPath"] != "" || actual["last"]["nextListUuid"] != nil {
				t.Fatalf("spliced rows: %#v", actual)
			}
			if !reflect.DeepEqual(actual["first"]["rules"], []byte{1, 2}) || !reflect.DeepEqual(actual["last"]["rules"], []byte{3, 4}) {
				t.Fatal("splicing changed rules")
			}
			requireSavedGraph(t, d)
		})
	}
}

func TestSmartlistRetainedParentRemovalRefuses(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Folder',0,0)`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Folder',0,0),(49,'Child',48,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('kept','Kept','Folder;Child;',NULL,NULL,'rules')`)
	before := savedRows(t, d)
	r, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{12}}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}}, Mirror: true, Conflict: "source", NoBackup: true})
	if err == nil || r == nil || r.Applicable || !reflect.DeepEqual(before, savedRows(t, d)) {
		t.Fatalf("retained parent removed: %+v, %v", r, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM Playlist WHERE id=49`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("refusal lost native parent: %d, %v", count, err)
	}
}

func TestSmartlistFinalRowsRejectTriggerChanges(t *testing.T) {
	for _, statement := range []string{
		`UPDATE Smartlist SET rules='unexpected' WHERE listUuid=NEW.listUuid;`,
		`UPDATE Smartlist SET nextListUuid='missing' WHERE listUuid=NEW.listUuid;`,
		`DELETE FROM Smartlist WHERE listUuid=NEW.listUuid;`,
	} {
		t.Run(statement, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','Saved',NULL,NULL,NULL,x'007fff')`)
			execSQL(t, d, `CREATE TRIGGER damage_synced_saved AFTER INSERT ON Smartlist BEGIN `+statement+` END`)
			if _, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true}); err == nil {
				t.Fatal("accepted an unreviewed trigger change")
			}
			if actual := savedRows(t, d); len(actual) != 0 {
				t.Fatalf("failed transaction retained rows: %#v", actual)
			}
		})
	}
}
