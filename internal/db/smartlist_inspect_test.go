package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func smartlistInspectionLibrary(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if _, err := CreateLibrary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, p := range []struct {
		title  string
		parent int64
	}{{"House", 0}, {"Deep", 1}, {"Household", 0}} {
		if _, err := CreatePlaylist(context.Background(), d, p.title, p.parent, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES ('a','Saved A','House;','','b','{"opaque":"λ"}'),('b','Saved B','House;','House;Deep;','','unchanged')`); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInspectSmartlistsNativeDamage(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		want           map[string][]string
	}{
		{"healthy", "", map[string][]string{}},
		{"healthy root parent", `UPDATE Smartlist SET parentPlaylistPath=''; UPDATE Smartlist SET nextPlaylistPath='House;' WHERE listUuid='b'`, map[string][]string{}},
		{"cross-parent saved link", `UPDATE Smartlist SET parentPlaylistPath='Household;',nextPlaylistPath='' WHERE listUuid='b'`, map[string][]string{"a": {"sibling_link_invalid"}}},
		{"simultaneous sibling targets", `UPDATE Smartlist SET nextPlaylistPath='House;Deep;' WHERE listUuid='a'`, map[string][]string{"a": {"sibling_link_invalid"}}},
		{"duplicate predecessor", `CREATE TABLE copy AS SELECT * FROM Smartlist; DROP TABLE Smartlist; ALTER TABLE copy RENAME TO Smartlist; INSERT INTO Smartlist SELECT 'c','Saved C',parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime FROM Smartlist WHERE listUuid='a'`, map[string][]string{"a": {"sibling_link_invalid"}, "c": {"sibling_link_invalid"}}},
		{"renamed parent", `UPDATE Playlist SET title='Renamed' WHERE id=1`, map[string][]string{"a": {"parent_path_unresolved"}, "b": {"parent_path_unresolved", "next_path_unresolved"}}},
		{"moved nested parent", `UPDATE Playlist SET parentListId=3 WHERE id=1`, map[string][]string{"a": {"parent_path_unresolved"}, "b": {"parent_path_unresolved", "next_path_unresolved"}}},
		{"stale next", `UPDATE Smartlist SET nextPlaylistPath='House;Gone;' WHERE listUuid='b'`, map[string][]string{"b": {"next_path_unresolved"}}},
		{"wrong parent next", `UPDATE Smartlist SET nextPlaylistPath='Household;' WHERE listUuid='b'`, map[string][]string{"b": {"next_path_unresolved"}}},
		{"ambiguous native separator path", `INSERT INTO Playlist(title,parentListId,nextListId) VALUES ('House;Deep',0,0)`, map[string][]string{"b": {"next_path_unresolved"}}},
		{"broken saved link", `UPDATE Smartlist SET nextListUuid='gone' WHERE listUuid='a'`, map[string][]string{"a": {"sibling_link_invalid"}}},
		{"cycle", `UPDATE Smartlist SET nextPlaylistPath='',nextListUuid='a' WHERE listUuid='b'`, map[string][]string{"a": {"sibling_cycle"}, "b": {"sibling_cycle"}}},
		{"deleted parent", `DELETE FROM Playlist WHERE id=1`, map[string][]string{"a": {"parent_path_unresolved"}, "b": {"parent_path_unresolved", "next_path_unresolved"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := smartlistInspectionLibrary(t)
			if tc.mutation != "" {
				if _, err := d.Exec(tc.mutation); err != nil {
					t.Fatal(err)
				}
			}
			before := inspectionRows(t, d)
			for range 2 {
				issues, err := InspectSmartlists(context.Background(), d)
				if err != nil {
					t.Fatal(err)
				}
				got := map[string][]string{}
				for _, issue := range issues {
					got[issue.UUID] = append(got[issue.UUID], issue.Kind)
					if issue.Name == "" {
						t.Fatal("missing saved name")
					}
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("issues=%v want=%v", got, tc.want)
				}
			}
			if after := inspectionRows(t, d); !reflect.DeepEqual(after, before) {
				t.Fatalf("read changed saved rows: %v -> %v", before, after)
			}
		})
	}
}

func TestInspectSmartlistsDuplicateIdentityFailsClosed(t *testing.T) {
	d := smartlistInspectionLibrary(t)
	if _, err := d.Exec(`CREATE TABLE copy AS SELECT * FROM Smartlist; DROP TABLE Smartlist; ALTER TABLE copy RENAME TO Smartlist; INSERT INTO Smartlist SELECT listUuid,'Duplicate name',parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime FROM Smartlist WHERE listUuid='a'`); err != nil {
		t.Fatal(err)
	}
	before := inspectionRows(t, d)
	if issues, err := InspectSmartlists(context.Background(), d); err == nil || issues != nil {
		t.Fatalf("accepted duplicate identities: issues=%v error=%v", issues, err)
	}
	if after := inspectionRows(t, d); !reflect.DeepEqual(before, after) {
		t.Fatalf("malformed schema rejection changed rows: %v -> %v", before, after)
	}
}

func TestReadSmartlistInventoryNativeIdentity(t *testing.T) {
	d := smartlistInspectionLibrary(t)
	if _, err := d.Exec(`UPDATE Playlist SET title='Renamed' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	before := inspectionRows(t, d)
	inventory, err := ReadSmartlistInventory(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var uuid string
	if err := d.QueryRow(`SELECT uuid FROM Information WHERE id=1`).Scan(&uuid); err != nil {
		t.Fatal(err)
	}
	if inventory.EngineUUID != uuid || len(inventory.Playlists) != 3 || len(inventory.Saved) != 2 || len(inventory.Issues) != 3 {
		t.Fatalf("incomplete native evidence: %+v", inventory)
	}
	paths := map[int64]string{}
	for _, playlist := range inventory.Playlists {
		paths[playlist.ID] = playlist.Path
		if playlist.ID == 2 && playlist.ParentID != 1 {
			t.Fatalf("lost nested identity: %+v", playlist)
		}
	}
	if !reflect.DeepEqual(paths, map[int64]string{1: "Renamed;", 2: "Renamed;Deep;", 3: "Household;"}) {
		t.Fatalf("native paths: %v", paths)
	}
	if inventory.Saved[0].UUID != "a" || inventory.Saved[0].ParentPath != "House;" || inventory.Saved[0].NextUUID != "b" || inventory.Saved[1].NextPath != "House;Deep;" {
		t.Fatalf("stored evidence was normalized: %+v", inventory.Saved)
	}
	if !reflect.DeepEqual(before, inspectionRows(t, d)) {
		t.Fatal("inventory changed native rows")
	}
	for _, mutation := range []string{`UPDATE Information SET uuid=''`, `DELETE FROM Information`} {
		if _, err := d.Exec(mutation); err != nil {
			t.Fatal(err)
		}
		if inventory, err := ReadSmartlistInventory(context.Background(), d); err == nil || inventory != nil {
			t.Fatalf("accepted unavailable identity: %+v %v", inventory, err)
		}
	}
}

func inspectionRows(t *testing.T, d *sql.DB) [][6]string {
	t.Helper()
	rows, err := d.Query(`SELECT listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules FROM Smartlist ORDER BY listUuid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [][6]string
	for rows.Next() {
		var row [6]string
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5]); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInspectSmartlistsAbsentMalformedAndCancelled(t *testing.T) {
	d := smartlistInspectionLibrary(t)
	if _, err := d.Exec(`DROP TABLE Smartlist`); err != nil {
		t.Fatal(err)
	}
	if issues, err := InspectSmartlists(context.Background(), d); err != nil || len(issues) != 0 {
		t.Fatalf("absent table: %v %v", issues, err)
	}
	if _, err := d.Exec(`CREATE VIEW Smartlist AS SELECT 1 AS listUuid`); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSmartlists(context.Background(), d); err == nil {
		t.Fatal("accepted wrong schema object")
	}
	if _, err := d.Exec(`DROP VIEW Smartlist; CREATE TABLE Smartlist(listUuid TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSmartlists(context.Background(), d); err == nil {
		t.Fatal("accepted missing native columns")
	}
	native := smartlistInspectionLibrary(t)
	if _, err := native.Exec(`UPDATE Smartlist SET parentPlaylistPath=NULL WHERE listUuid='a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSmartlists(context.Background(), native); err != nil {
		t.Fatalf("rejected native nullable parent path: %v", err)
	}
	inventory, err := ReadSmartlistInventory(context.Background(), native)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range inventory.Saved {
		if row.UUID == "a" {
			found = true
			if row.ParentPath != "" {
				t.Fatalf("native NULL parent did not resolve to root: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("nullable saved row disappeared from inventory")
	}
	var unchanged bool
	if err := native.QueryRow(`SELECT parentPlaylistPath IS NULL AND title='Saved A' AND nextPlaylistPath='' AND nextListUuid='b' AND rules='{"opaque":"λ"}' FROM Smartlist WHERE listUuid='a'`).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("inspection changed native nullable row: %v %v", unchanged, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectSmartlists(ctx, d); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := InspectSmartlists(context.Background(), nil); err == nil {
		t.Fatal("accepted nil database")
	}
}
