package api

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

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
