package transfer

import (
	"context"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

type sourceSmartlistShape struct {
	name, setup, operation, mutation, parent, next string
	omitted                                        int
}

// A renamed parent already has preview/apply/Undo coverage in
// TestSmartlistSyncProjectionPreservesSourceAndDestinationUndo. These cases
// exercise the remaining recovery shapes through the complete transfer owner.
func TestSmartlistSyncDamagedSourceShapes(t *testing.T) {
	for _, tc := range []sourceSmartlistShape{
		{
			name: "moved parent", operation: "playlist.move",
			mutation: `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`,
			parent:   "Sets;House;",
		},
		{
			name:      "nested renamed ancestor",
			setup:     `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(3,'Child',1,0); UPDATE Smartlist SET parentPlaylistPath='House;Child;' WHERE listUuid='saved'`,
			operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`,
			parent: "Renamed;Child;",
		},
		{
			name:      "stale ordinary successor",
			setup:     `UPDATE Smartlist SET parentPlaylistPath='',nextPlaylistPath='House;' WHERE listUuid='saved'`,
			operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`,
			next: "Renamed;",
		},
		{
			name: "deleted parent", operation: "playlist.rm",
			mutation: `DELETE FROM Playlist WHERE id=1`, omitted: 1,
		},
		{
			name:      "broken chain",
			setup:     `UPDATE Smartlist SET nextListUuid='tail' WHERE listUuid='saved'; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('tail','Tail','House;','Missing;','','tail rules')`,
			operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`, omitted: 2,
		},
		{
			name:      "cycle",
			setup:     `UPDATE Smartlist SET nextListUuid='tail' WHERE listUuid='saved'; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('tail','Tail','House;','','saved','tail rules')`,
			operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`, omitted: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, want := sourceShapeLibrary(t, tc)
			destination, database := library(t, "destination")
			beforeSource := syncFileManifest(t, source)
			beforeDestination := syncFileManifest(t, destination)
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
			preview, err := Run(t.Context(), o)
			if err != nil || preview == nil || !preview.Applicable || preview.PlanToken == "" || preview.OperationID != "" {
				t.Fatalf("preview: %+v, %v", preview, err)
			}
			reconnected := 1
			if tc.omitted != 0 {
				reconnected = 0
			}
			if preview.Summary.SourceSmartlistsReconnected != reconnected || preview.Summary.SourceSmartlistsOmitted != tc.omitted || preview.Summary.SmartlistsAdded != len(want) {
				t.Fatalf("source projection counts: %+v", preview.Summary)
			}
			requireSourceShapeActions(t, preview, tc.omitted)
			if !reflect.DeepEqual(beforeSource, syncFileManifest(t, source)) || !reflect.DeepEqual(beforeDestination, syncFileManifest(t, destination)) {
				t.Fatal("preview changed source evidence or destination storage")
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			applied, err := Run(t.Context(), o)
			if err != nil || applied == nil || applied.OperationID == "" || applied.PlanToken != preview.PlanToken || !reflect.DeepEqual(applied.Changes, preview.Changes) {
				t.Fatalf("apply diverged from preview: %+v, %v", applied, err)
			}
			if actual := savedRows(t, database); !reflect.DeepEqual(actual, want) {
				t.Fatalf("destination saved rows:\n got %#v\nwant %#v", actual, want)
			}
			requireSavedGraph(t, database)
			if !reflect.DeepEqual(beforeSource, syncFileManifest(t, source)) {
				t.Fatal("apply changed source files, including native rows or History evidence")
			}
		})
	}
}

func sourceShapeLibrary(t *testing.T, shape sourceSmartlistShape) (string, map[string]row) {
	t.Helper()
	lib, database := library(t, "source")
	execSQL(t, database, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(1,'House',0,2),(2,'Sets',0,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','Saved','House;','','',x'00ff0a'),('healthy','Healthy','Sets;',NULL,NULL,' { "opaque": true } ')`)
	if shape.setup != "" {
		execSQL(t, database, shape.setup)
	}
	entry, err := history.Run(t.Context(), lib, shape.operation, history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := database.ExecContext(ctx, shape.mutation)
		return err
	})
	if err != nil || entry == nil || entry.After == nil {
		t.Fatalf("record source damage: %+v, %v", entry, err)
	}
	want := savedRows(t, database)
	if shape.omitted != 0 {
		delete(want, "saved")
		delete(want, "tail")
	} else {
		want["saved"]["parentPlaylistPath"] = shape.parent
		want["saved"]["nextPlaylistPath"] = shape.next
	}
	// Close the fixture writer before hashing so the comparison covers stable
	// native and History content, not an open writer's coordination files.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return lib, want
}

func requireSourceShapeActions(t *testing.T, report *Report, omitted int) {
	t.Helper()
	want := map[string]string{"smartlist:saved": "reconnect"}
	if omitted != 0 {
		want["smartlist:saved"] = "omit"
	}
	if omitted == 2 {
		want["smartlist:tail"] = "omit"
	}
	actual := map[string]string{}
	for _, change := range report.Changes {
		if change.Action != "omit" && change.Action != "reconnect" {
			continue
		}
		if change.Entity != "smartlist" || change.Label == "" || change.Reason == "" || change.Code == "" {
			t.Fatalf("unexplained source recovery: %+v", change)
		}
		actual[change.ID] = change.Action
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("source recovery actions: got %+v, want %+v", actual, want)
	}
}
