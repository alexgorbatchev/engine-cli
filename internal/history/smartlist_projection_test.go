package history

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func projectionForTest(t *testing.T, lib string, database *sql.DB) *SmartlistProjection {
	t.Helper()
	projection, err := ReadSmartlistProjection(t.Context(), lib, database, func(_ context.Context, _ *sql.Tx, projection *SmartlistProjection) (*SmartlistProjection, error) {
		return projection, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func TestSmartlistProjectionNativeDamageAndReadOnlySource(t *testing.T) {
	for _, tc := range []struct {
		name, setup, operation, mutation, parent, next string
		parentID, nextID                               int64
		omitted                                        int
	}{
		{name: "renamed parent", operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`, parent: "Renamed;", parentID: 1},
		{name: "moved parent", operation: "playlist.move", mutation: `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`, parent: "Sets;House;", parentID: 1},
		{name: "nested descendant", setup: `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(3,'Child',1,0); UPDATE Smartlist SET parentPlaylistPath='House;Child;'`, operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`, parent: "Renamed;Child;", parentID: 3},
		{name: "semicolon title", setup: `UPDATE Playlist SET title='House;Prefix' WHERE id=1; UPDATE Smartlist SET parentPlaylistPath='House;Prefix;'`, operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Changed;Prefix' WHERE id=1`, parent: "Changed;Prefix;", parentID: 1},
		{name: "stale next path", setup: `UPDATE Smartlist SET parentPlaylistPath='',nextPlaylistPath='House;'`, operation: "playlist.rename", mutation: `UPDATE Playlist SET title='Renamed' WHERE id=1`, next: "Renamed;", nextID: 1},
		{name: "deleted parent", operation: "playlist.rm", mutation: `DELETE FROM Playlist WHERE id=1`, omitted: 1},
		{name: "unrecorded parent", setup: `UPDATE Smartlist SET parentPlaylistPath='Missing;'`, omitted: 1},
		{name: "broken chain", setup: `UPDATE Smartlist SET nextListUuid='tail'; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('tail','Tail','House;','Missing;','','tail rules')`, omitted: 2},
		{name: "cycle", setup: `UPDATE Smartlist SET nextListUuid='tail'; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('tail','Tail','House;','','saved','tail rules')`, omitted: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			if tc.setup != "" {
				if _, err := database.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := database.Exec(`ALTER TABLE Smartlist ADD COLUMN opaqueFuture BLOB; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('healthy','Healthy','Sets;',NULL,NULL,'healthy rules')`); err != nil {
				t.Fatal(err)
			}
			opaque := []byte{0, 0xff, 0xfe, 'r'}
			if _, err := database.Exec(`UPDATE Smartlist SET rules=?,opaqueFuture=? WHERE listUuid='saved'`, opaque, opaque); err != nil {
				t.Fatal(err)
			}
			if tc.mutation != "" {
				recordSmartlistMutation(t, lib, database, tc.operation, tc.mutation)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lib, "Database2", "m.db")
			readOnly, err := db.Open(path, true)
			if err != nil {
				t.Fatal(err)
			}
			defer readOnly.Close()
			// Keep the native behavior matrix active where Unix permissions do
			// not apply; the connection itself is read-only on every platform.
			if runtime.GOOS != "windows" && os.Geteuid() != 0 {
				smartlistRestrict(t, path, 0400)
				smartlistRestrict(t, filepath.Dir(path), 0500)
				smartlistRestrict(t, lib, 0500)
			}
			before := historyLeaseTree(t, lib)
			projection := projectionForTest(t, lib, readOnly)
			if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
				t.Fatalf("projection changed source files, metadata, or enrolled storage:\n%s", historyLeaseTreeDiff(before, after))
			}
			if projection.EvidenceDigest == "" || len(projection.Actions) != max(1, tc.omitted) {
				t.Fatalf("missing reviewed evidence/actions: %+v", projection)
			}
			wantRows := 2
			if tc.omitted != 0 {
				wantRows = 1
			}
			if len(projection.Rows) != wantRows {
				t.Fatalf("projected %d rows, want %d: %+v", len(projection.Rows), wantRows, projection.Rows)
			}
			for _, row := range projection.Rows {
				if row.Values["listUuid"] == "healthy" {
					if row.Values["nextPlaylistPath"] != nil || row.Values["nextListUuid"] != nil || row.Values["rules"] != "healthy rules" || row.ParentPlaylistID != 2 {
						t.Fatalf("healthy native values changed: %+v", row)
					}
					continue
				}
				if row.Values["listUuid"] != "saved" || row.Values["parentPlaylistPath"] != tc.parent || row.Values["nextPlaylistPath"] != tc.next || row.ParentPlaylistID != tc.parentID || row.NextPlaylistID != tc.nextID {
					t.Fatalf("incorrect projected linkage: %+v", row)
				}
				for _, field := range []string{"rules", "opaqueFuture"} {
					blob, ok := row.Values[field].([]byte)
					if !ok || !bytes.Equal(blob, opaque) {
						t.Fatalf("opaque %s changed: %#v", field, row.Values[field])
					}
					blob[0] = 42
				}
			}
			for _, action := range projection.Actions {
				want := "reconnect"
				if tc.omitted != 0 {
					want = "omit"
				}
				if action.Action != want || action.Name == "" || action.DeleteEntryID != "" {
					t.Fatalf("source projection promised mutation/Undo: %+v", action)
				}
			}
			if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
				t.Fatalf("editing returned values changed source storage:\n%s", historyLeaseTreeDiff(before, after))
			}
		})
	}
}

func TestSmartlistProjectionHealthyNullableAndAbsentTable(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "nullable", true: "absent"}[absent], func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			query := `UPDATE Smartlist SET parentPlaylistPath=NULL,nextPlaylistPath=NULL,nextListUuid=NULL`
			if absent {
				query = `DROP TABLE Smartlist`
			}
			if _, err := database.Exec(query); err != nil {
				t.Fatal(err)
			}
			before := historyLeaseTree(t, lib)
			projection := projectionForTest(t, lib, database)
			if len(projection.Actions) != 0 || (!absent && len(projection.Rows) != 1) || (absent && len(projection.Rows) != 0) {
				t.Fatalf("healthy/absent projection=%+v", projection)
			}
			if !absent {
				for _, field := range []string{"parentPlaylistPath", "nextPlaylistPath", "nextListUuid"} {
					if projection.Rows[0].Values[field] != nil {
						t.Fatalf("normalized native NULL in %s", field)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("healthy projection enrolled source: %v", err)
			}
			if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
				t.Fatalf("healthy projection changed source:\n%s", historyLeaseTreeDiff(before, after))
			}
		})
	}
}
