package transfer

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestMirrorPreservesMappedChildOfRemovedParent(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Moved',0,0)`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Old',0,0),(49,'Moved',48,0)`)
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 49}}, Mirror: true, Conflict: "source", NoBackup: true, DryRun: true}
	preview, err := Run(t.Context(), o)
	if err != nil || !preview.Applicable {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	o.DryRun = false
	result, err := Run(t.Context(), o)
	if err != nil || !result.Applicable {
		t.Fatalf("apply: %+v, %v", result, err)
	}
	var id, parent, count int
	if err := d.QueryRow(`SELECT id,parentListId FROM Playlist WHERE title='Moved'`).Scan(&id, &parent); err != nil || id != 49 || parent != 0 {
		t.Fatalf("retained child: id=%d parent=%d err=%v", id, parent, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM Playlist`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remaining playlists: %d, %v", count, err)
	}
}

func mirrorNativeRows(t *testing.T, d *sql.DB) map[string][]row {
	t.Helper()
	result := map[string][]row{}
	for _, table := range []string{"Playlist", "PlaylistEntity", "Track", "PerformanceData", "Smartlist"} {
		values, _, err := rows(t.Context(), d, table)
		if err != nil {
			t.Fatal(err)
		}
		result[table] = values
	}
	return result
}

func TestMirrorRetainedDescendantsMembershipOrderAndUndo(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Moved',0,15),(13,'Following',0,0),(14,'Nested',12,0),(15,'Moved too',0,13);
 INSERT INTO Track(id,title,path,filename) VALUES(1,'Track','song.mp3','song.mp3');
 INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(12,1,'source',0),(14,1,'source',0)`)
	put(t, filepath.Join(source, "song.mp3"), "audio")
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Old',0,53),(49,'Moved',48,51),(50,'Nested',49,0),(51,'Deleted',48,0),(52,'Moved too',51,0),(53,'Following',0,0)`)
	before := mirrorNativeRows(t, d)
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Mirror: true, Conflict: "source", NoBackup: true, PlaylistMap: []PlaylistMapping{{12, 49}, {13, 53}, {14, 50}, {15, 52}}}
	applied, err := Run(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int][2]int{49: {0, 52}, 52: {0, 53}, 53: {0, 0}, 50: {49, 0}} {
		var parent, next int
		if err := d.QueryRow(`SELECT parentListId,nextListId FROM Playlist WHERE id=?`, id).Scan(&parent, &next); err != nil || [2]int{parent, next} != want {
			t.Fatalf("playlist %d: parent=%d next=%d err=%v", id, parent, next, err)
		}
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM Playlist`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("remaining: %d %v", count, err)
	}
	for _, id := range []int{49, 50} {
		if err := d.QueryRow(`SELECT count(*) FROM PlaylistEntity e JOIN Track t ON t.id=e.trackId WHERE e.listId=? AND t.title='Track'`, id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("membership %d: %d %v", id, count, err)
		}
	}
	if _, err := history.Undo(t.Context(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := mirrorNativeRows(t, d); !reflect.DeepEqual(before, after) {
		t.Fatalf("Undo changed native rows: before=%v after=%v", before, after)
	}
}

func TestMirrorRetainedChildRejectedWriteRollsBack(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Moved',0,0)`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Old',0,0),(49,'Moved',48,0);
 CREATE TRIGGER refuse_move BEFORE UPDATE ON Playlist WHEN NEW.id=49 AND NEW.parentListId=0 BEGIN SELECT RAISE(ABORT,'refused final move'); END`)
	before := mirrorNativeRows(t, d)
	_, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Mirror: true, Conflict: "source", NoBackup: true, PlaylistMap: []PlaylistMapping{{12, 49}}})
	if err == nil {
		t.Fatal("rejected write succeeded")
	}
	if after := mirrorNativeRows(t, d); !reflect.DeepEqual(before, after) {
		t.Fatal("failed publication changed native rows")
	}
}

func TestMirrorRetainedChildConflictPolicies(t *testing.T) {
	for _, policy := range []string{"error", "destination"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Moved',0,0)`)
			execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Old',0,0),(49,'Moved',48,0)`)
			before := mirrorNativeRows(t, d)
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Mirror: true, Conflict: policy, NoBackup: true, PlaylistMap: []PlaylistMapping{{12, 49}}}
			result, err := Run(t.Context(), o)
			if err == nil || result == nil || result.Applicable {
				t.Fatalf("invalid retained-parent plan accepted: %+v %v", result, err)
			}
			if !reflect.DeepEqual(before, mirrorNativeRows(t, d)) {
				t.Fatal("conflict changed native rows")
			}
		})
	}
}

func TestMirrorRetainedChildCancellationRollsBack(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Moved',0,0)`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Old',0,0),(49,'Moved',48,0)`)
	before := mirrorNativeRows(t, d)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reached := false
	ctx = progress.WithReporter(ctx, func(event progress.Event) {
		if event.Phase == "sync.finalize" {
			reached = true
			cancel()
		}
	})
	_, err := Run(ctx, Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Mirror: true, Conflict: "source", NoBackup: true, PlaylistMap: []PlaylistMapping{{12, 49}}})
	if !reached || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not reached: %t %v", reached, err)
	}
	if !reflect.DeepEqual(before, mirrorNativeRows(t, d)) {
		t.Fatal("cancelled publication changed native rows")
	}
}

func TestMirrorRetainedChildPersistsNewAncestors(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId,isPersisted) VALUES(12,'Old',0,14,1),(13,'Child',12,0,1),(14,'Target',0,0,1)`)
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Mirror: true, Conflict: "source", NoBackup: true}
	if _, err := Run(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	execSQL(t, d, `UPDATE Playlist SET isPersisted=0 WHERE title='Target'`)
	execSQL(t, s, `UPDATE Playlist SET parentListId=14 WHERE id=13; DELETE FROM Playlist WHERE id=12`)
	o.Conflict = "destination"
	if _, err := Run(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	var persisted int
	if err := d.QueryRow(`SELECT isPersisted FROM Playlist WHERE title='Target'`).Scan(&persisted); err != nil || persisted != 1 {
		t.Fatalf("native parent persistence lost: %d %v", persisted, err)
	}
}
