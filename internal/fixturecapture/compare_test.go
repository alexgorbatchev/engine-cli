package fixturecapture

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureComparesNativeRowIdentityAndLinks(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"before", "after"} {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", sqliteURI(filepath.Join(dir, "m.db"), "rwc"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, title TEXT, parentPlaylistPath TEXT, nextPlaylistPath TEXT, nextListUuid TEXT, rules TEXT, lastEditTime DATETIME);
CREATE TABLE Playlist (id INTEGER PRIMARY KEY, title TEXT, parentListId INTEGER, nextListId INTEGER);
CREATE TABLE PlaylistEntity (id INTEGER PRIMARY KEY, listId INTEGER, trackId INTEGER, nextEntityId INTEGER);
CREATE TABLE Storage (v BLOB, n REAL);
INSERT INTO Smartlist VALUES ('keep','Kept',NULL,'','remove','{"rules":[]}','2026-10-31 12:00:00');
INSERT INTO Smartlist VALUES ('remove','Removed','','',NULL,'{}',NULL);
INSERT INTO Playlist VALUES (4,'Parent',0,0);
INSERT INTO PlaylistEntity VALUES (1,4,10,2),(2,4,20,0);
INSERT INTO Storage VALUES (x'00FF',1.25);`)
		if err == nil && name == "after" {
			_, err = db.Exec(`UPDATE Smartlist SET parentPlaylistPath='', nextListUuid='new' WHERE listUuid='keep'; DELETE FROM Smartlist WHERE listUuid='remove'; INSERT INTO Smartlist VALUES ('new','New','Parent;','','','{}',NULL); UPDATE Playlist SET title='Renamed' WHERE id=4; UPDATE PlaylistEntity SET nextEntityId=3 WHERE id=1; DELETE FROM PlaylistEntity WHERE id=2; INSERT INTO PlaylistEntity VALUES (3,4,30,0);`)
		}
		closeErr := db.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	out := filepath.Join(base, "capture")
	if _, err := Capture(t.Context(), filepath.Join(base, "before"), filepath.Join(base, "after"), out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "row-changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var changes []Change
	if err := json.Unmarshal(data, &changes); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 7 {
		t.Fatalf("expected 3 Smartlist, 1 Playlist, 3 entity changes, got %#v", changes)
	}
	var link, insert, deleted bool
	for _, change := range changes {
		if change.Table == "Smartlist" && change.Key.Data == base64.StdEncoding.EncodeToString([]byte("keep")) {
			link = true
			if change.Before[2].Type != "null" || change.After[2].Type != "text" || change.After[2].Data != "" || change.Before[5] != change.After[5] {
				t.Fatalf("lost NULL/empty or unchanged Rules: %#v", change)
			}
		}
		if change.Table == "PlaylistEntity" && change.Key.Data == "3" {
			insert = change.Before == nil && change.After[1].Data == "4"
		}
		if change.Table == "PlaylistEntity" && change.Key.Data == "2" {
			deleted = change.Before[1].Data == "4" && change.After == nil
		}
	}
	if !link || !insert || !deleted {
		t.Fatalf("link/insert/delete missing: %#v", changes)
	}
	tables, err := readTables(filepath.Join(out, "snapshots", "before", "m.db.json"))
	if err != nil {
		t.Fatal(err)
	}
	storage := findTable(tables, "Storage")
	if storage == nil || storage.Rows[0][0].Type != "blob" || storage.Rows[0][0].Data != "AP8=" || storage.Rows[0][1] != (Value{Type: "real", Data: "1.25"}) {
		t.Fatalf("storage types changed: %#v", storage)
	}
	smartlist := findTable(tables, "Smartlist")
	if smartlist.Rows[0][6].Type != "text" {
		t.Fatalf("DATETIME converted: %#v", smartlist.Rows[0][6])
	}
}
