package api

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func mixedNativeState(t *testing.T, d *sql.DB) string {
	t.Helper()
	var state string
	err := d.QueryRow(`SELECT json_array(
 (SELECT json_group_array(json_array(id,title,parentListId,nextListId,lastEditTime)) FROM (SELECT * FROM Playlist ORDER BY id)),
 (SELECT json_group_array(json_array(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime)) FROM (SELECT * FROM Smartlist ORDER BY listUuid)))`).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestMixedPlaylistMoveAndExactUndo(t *testing.T) {
	for _, tc := range []struct {
		before string
		want   []string
	}{
		{"smart:b", []string{"Preceding", "A", "Favorites", "B", "Last", "Trailing"}},
		{"smart:p", []string{"Favorites", "Preceding", "A", "B", "Last", "Trailing"}},
		{"2", []string{"Preceding", "A", "B", "Favorites", "Last", "Trailing"}},
		{"", []string{"Preceding", "A", "B", "Last", "Trailing", "Favorites"}},
	} {
		t.Run(tc.before, func(t *testing.T) {
			s, library := testAPI(t)
			d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			_, err = d.Exec(`UPDATE Playlist SET nextListId=2 WHERE id=1;
    INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(2,'Last',0,0);
    INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES
    ('p','Preceding','','Favorites;','','opaque p','2026-01-01'),
    ('a','A','','','b','opaque a','2026-01-02'),
    ('b','B','','Last;','','opaque b','2026-01-03'),
    ('c','Trailing','','','','opaque c','2026-01-04')`)
			if err != nil {
				t.Fatal(err)
			}
			original := mixedNativeState(t, d)
			body, err := json.Marshal(map[string]string{"identifier": "1", "before": tc.before})
			if err != nil {
				t.Fatal(err)
			}
			job := runAPIJob(t, s, "playlist.move", string(body))
			tree, err := db.ReadPlaylistTree(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, node := range tree.Nodes {
				names = append(names, node.Title)
			}
			if !reflect.DeepEqual(names, tc.want) || len(tree.Issues) != 0 {
				t.Fatalf("tree=%v issues=%v want=%v", names, tree.Issues, tc.want)
			}
			var rules string
			if err := d.QueryRow(`SELECT group_concat(rules,'|') FROM (SELECT rules FROM Smartlist ORDER BY listUuid)`).Scan(&rules); err != nil {
				t.Fatal(err)
			}
			if rules != "opaque a|opaque b|opaque c|opaque p" {
				t.Fatalf("rules changed: %q", rules)
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
			if got := mixedNativeState(t, d); got != original {
				t.Fatalf("Undo changed native rows\ngot %s\nwant %s", got, original)
			}
		})
	}
}

func TestPlaylistSmartlistAPIAndUndo(t *testing.T) {
	for _, tc := range []struct {
		operation, body, wantParent string
		wantCount                   int
	}{
		{"playlist.rename", `{"identifier":"1","title":"Renamed"}`, "Renamed;Child;", 1},
		{"playlist.move", `{"identifier":"1","to":"3"}`, "Destination;Favorites;Child;", 1},
		{"playlist.rm", `{"identifier":"1","force":true}`, "", 0},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			s, library := testAPI(t)
			d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec(`UPDATE Playlist SET nextListId=3 WHERE id=1;
			INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(2,'Child',1,0),(3,'Destination',0,0);
			INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES('saved','Saved','Favorites;Child;','','','opaque rules','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			before, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			job := runAPIJob(t, s, tc.operation, tc.body)
			var count int
			if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != tc.wantCount {
				t.Fatalf("Smartlist count=%d want=%d", count, tc.wantCount)
			}
			if tc.wantCount != 0 {
				var parent string
				if err := d.QueryRow(`SELECT parentPlaylistPath FROM Smartlist WHERE listUuid='saved'`).Scan(&parent); err != nil {
					t.Fatal(err)
				}
				if parent != tc.wantParent {
					t.Fatalf("parent=%q want=%q", parent, tc.wantParent)
				}
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
			after, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			var parent, rules, edited string
			if err := d.QueryRow(`SELECT parentPlaylistPath,rules,lastEditTime FROM Smartlist WHERE listUuid='saved'`).Scan(&parent, &rules, &edited); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || parent != "Favorites;Child;" || rules != "opaque rules" || edited != "2026-01-01T00:00:00Z" {
				t.Fatalf("Undo did not restore native rows: playlists=%v parent=%q rules=%q edited=%q", after, parent, rules, edited)
			}
		})
	}
}

func TestPlaylistBeforeSmartlistSchemaAndUndo(t *testing.T) {
	for _, tc := range []struct{ operation, body string }{
		{"playlist.rename", `{"identifier":"1","title":"Renamed"}`},
		{"playlist.move", `{"identifier":"1","to":"3"}`},
		{"playlist.rm", `{"identifier":"1","force":true}`},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			s, library := testAPI(t)
			d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec(`DROP TABLE Smartlist;
			ALTER TABLE Information ADD COLUMN schemaVersionMajor INTEGER;
			ALTER TABLE Information ADD COLUMN schemaVersionMinor INTEGER;
			ALTER TABLE Information ADD COLUMN schemaVersionPatch INTEGER;
			UPDATE Information SET schemaVersionMajor=2,schemaVersionMinor=20,schemaVersionPatch=0;
			UPDATE Playlist SET nextListId=3 WHERE id=1;
			INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(2,'Child',1,0),(3,'Destination',0,0)`); err != nil {
				t.Fatal(err)
			}
			before, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			job := runAPIJob(t, s, tc.operation, tc.body)
			after, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(before, after) {
				t.Fatal("ordinary playlist mutation did not change the library")
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
			restored, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, restored) {
				t.Fatalf("Undo did not restore ordinary playlists: got=%v want=%v", restored, before)
			}
			var savedTable bool
			if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='Smartlist')`).Scan(&savedTable); err != nil {
				t.Fatal(err)
			}
			if savedTable {
				t.Fatal("playlist operation or Undo enrolled a Smartlist table")
			}
		})
	}
}

func TestSmartlistOnlyRemovalFailureCode(t *testing.T) {
	s, library := testAPI(t)
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','Saved','Favorites;','','','opaque')`); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "playlist.rm", `{"identifier":"1"}`))
	if job["state"] != "failed" || job["error"].(map[string]any)["code"] != "playlist_not_empty" || job["progress"].(map[string]any)["error_code"] != "playlist_not_empty" {
		t.Fatalf("nonempty refusal lacks stable failure code: %v", job)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Smartlist WHERE listUuid='saved'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("refused removal changed saved Smartlists")
	}
}
