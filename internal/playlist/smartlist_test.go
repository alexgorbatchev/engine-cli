package playlist

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func smartlistLibrary(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if _, err := db.CreateLibrary(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, p := range []struct {
		title  string
		parent int64
	}{{"Crates", 0}, {"House", 1}, {"Deep", 2}, {"Sets", 0}, {"House2", 1}, {"Household", 1}} {
		if _, err := db.CreatePlaylist(t.Context(), d, p.title, p.parent, false); err != nil {
			t.Fatal(err)
		}
	}
	_, err = d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES
	('before-root','before-root','','Crates;','','opaque-root','2026-01-01'),
	('before-house','before-house','Crates;','Crates;House;','','opaque-before','2026-01-01'),
	('tail','tail','Crates;','Crates;House2;','','opaque-tail','2026-01-01'),
	('tail-head','tail-head','Crates;','','tail','opaque-chain','2026-01-01'),
	('under-house','under-house','Crates;House;','Crates;House;Deep;','','opaque-house','2026-01-01'),
	('under-deep','under-deep','Crates;House;Deep;','','','opaque-deep','2026-01-01'),
	('destination-tail','destination-tail','Sets;','','','opaque-destination','2026-01-01'),
	('similar','similar','Crates;House2;','','','opaque-similar','2026-01-01'),
	('household','household','Crates;Household;','','','opaque-household','2026-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func smartlistState(t *testing.T, d *sql.DB) map[string][4]string {
	t.Helper()
	rows, err := d.Query(`SELECT listUuid,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules FROM Smartlist ORDER BY listUuid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string][4]string)
	for rows.Next() {
		var id string
		var value [4]string
		if err := rows.Scan(&id, &value[0], &value[1], &value[2], &value[3]); err != nil {
			t.Fatal(err)
		}
		result[id] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSmartlistPlaylistMutations(t *testing.T) {
	for _, name := range []string{"rename", "move", "delete"} {
		t.Run(name, func(t *testing.T) {
			d := smartlistLibrary(t)
			want := smartlistState(t, d)
			switch name {
			case "rename":
				if _, err := RenamePlaylist(t.Context(), d, "2", "Renamed"); err != nil {
					t.Fatal(err)
				}
				want["before-house"] = [4]string{"Crates;", "Crates;Renamed;", "", "opaque-before"}
				want["under-house"] = [4]string{"Crates;Renamed;", "Crates;Renamed;Deep;", "", "opaque-house"}
				want["under-deep"] = [4]string{"Crates;Renamed;Deep;", "", "", "opaque-deep"}
			case "move":
				if _, _, err := MovePlaylist(t.Context(), d, "2", "4", ""); err != nil {
					t.Fatal(err)
				}
				want["before-house"] = [4]string{"Crates;", "", "tail-head", "opaque-before"}
				want["destination-tail"] = [4]string{"Sets;", "Sets;House;", "", "opaque-destination"}
				want["under-house"] = [4]string{"Sets;House;", "Sets;House;Deep;", "", "opaque-house"}
				want["under-deep"] = [4]string{"Sets;House;Deep;", "", "", "opaque-deep"}
			case "delete":
				if _, err := DeletePlaylist(t.Context(), d, "2", true); err != nil {
					t.Fatal(err)
				}
				delete(want, "under-house")
				delete(want, "under-deep")
				want["before-house"] = [4]string{"Crates;", "", "tail-head", "opaque-before"}
			}
			if got := smartlistState(t, d); !reflect.DeepEqual(got, want) {
				t.Fatalf("Smartlist rows\ngot  %v\nwant %v", got, want)
			}
		})
	}
}

func TestSmartlistNativeTitlePaths(t *testing.T) {
	for _, title := range []string{"Housewarming", "50%_House", "House;Live", "家"} {
		t.Run(title, func(t *testing.T) {
			d := smartlistLibrary(t)
			if _, err := db.CreatePlaylist(t.Context(), d, "House", 4, false); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('other-house','other-house','Sets;House;','','','untouched')`); err != nil {
				t.Fatal(err)
			}
			if _, err := RenamePlaylist(t.Context(), d, "2", title); err != nil {
				t.Fatal(err)
			}
			var path string
			if err := d.QueryRow(`SELECT path FROM PlaylistPath WHERE id=3`).Scan(&path); err != nil {
				t.Fatal(err)
			}
			state := smartlistState(t, d)
			if state["under-deep"][0] != path || state["other-house"][0] != "Sets;House;" {
				t.Fatalf("native path=%q state=%v", path, state)
			}
			if _, err := DeletePlaylist(t.Context(), d, "2", true); err != nil {
				t.Fatal(err)
			}
			state = smartlistState(t, d)
			if _, exists := state["similar"]; !exists {
				t.Fatal("subtree deletion removed a different path prefix")
			}
			if _, exists := state["other-house"]; !exists {
				t.Fatal("subtree deletion removed same title under another parent")
			}
			if _, exists := state["household"]; !exists {
				t.Fatal("House subtree deletion removed Household")
			}
		})
	}
}

func TestSmartlistMutationRollback(t *testing.T) {
	for _, name := range []string{"rename", "move", "delete"} {
		t.Run(name, func(t *testing.T) {
			d := smartlistLibrary(t)
			want := smartlistState(t, d)
			playlists, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`CREATE TRIGGER reject_smartlist_update BEFORE UPDATE ON Smartlist BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "rename":
				_, err = RenamePlaylist(t.Context(), d, "2", "Renamed")
			case "move":
				_, _, err = MovePlaylist(t.Context(), d, "2", "4", "")
			case "delete":
				_, err = DeletePlaylist(t.Context(), d, "2", true)
			}
			if err == nil {
				t.Fatal("rejected Smartlist write succeeded")
			}
			got, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, playlists) || !reflect.DeepEqual(smartlistState(t, d), want) {
				t.Fatal("failed Smartlist mutation changed playlists or Smartlists")
			}
		})
	}
}

func TestSmartlistRenameInvalidAncestry(t *testing.T) {
	for _, parent := range []int64{3, 999} {
		t.Run(fmt.Sprintf("parent-%d", parent), func(t *testing.T) {
			d := smartlistLibrary(t)
			// Disable persistence while the hierarchy is still valid: Engine's
			// persistence trigger itself walks ancestors on a parent change.
			if _, err := d.Exec(`UPDATE Playlist SET isPersisted=0 WHERE id=2`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE Playlist SET parentListId=? WHERE id=2`, parent); err != nil {
				t.Fatal(err)
			}
			before := smartlistState(t, d)
			if err := db.RenamePlaylist(t.Context(), d, 2, "Rejected"); err == nil {
				t.Fatal("rename accepted invalid playlist ancestry")
			}
			var title string
			var gotParent int64
			if err := d.QueryRow(`SELECT title,parentListId FROM Playlist WHERE id=2`).Scan(&title, &gotParent); err != nil {
				t.Fatal(err)
			}
			if title != "House" || gotParent != parent || !reflect.DeepEqual(before, smartlistState(t, d)) {
				t.Fatal("refused rename changed playlist or Smartlist state")
			}
		})
	}
}

func TestSmartlistRenameStorageFailure(t *testing.T) {
	for _, tc := range []struct {
		name, setup, recover string
	}{
		{"missing Smartlist table", `ALTER TABLE Smartlist RENAME TO saved_smartlists`, `ALTER TABLE saved_smartlists RENAME TO Smartlist`},
		{"rejected playlist write", `CREATE TRIGGER reject_playlist_update BEFORE UPDATE ON Playlist BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`, `DROP TRIGGER reject_playlist_update`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := smartlistLibrary(t)
			before := smartlistState(t, d)
			if _, err := d.Exec(tc.setup); err != nil {
				t.Fatal(err)
			}
			if _, err := RenamePlaylist(t.Context(), d, "2", "Rejected"); err == nil {
				t.Fatal("rename succeeded despite failed storage operation")
			}
			if _, err := d.Exec(tc.recover); err != nil {
				t.Fatal(err)
			}
			var title string
			if err := d.QueryRow(`SELECT title FROM Playlist WHERE id=2`).Scan(&title); err != nil {
				t.Fatal(err)
			}
			if title != "House" || !reflect.DeepEqual(before, smartlistState(t, d)) {
				t.Fatal("failed rename changed playlist or Smartlist state")
			}
			if _, err := RenamePlaylist(t.Context(), d, "2", "Recovered"); err != nil {
				t.Fatalf("rename after storage recovery: %v", err)
			}
			if got := smartlistState(t, d)["under-deep"][0]; got != "Crates;Recovered;Deep;" {
				t.Fatalf("recovered rename left descendant path %q", got)
			}
		})
	}
}

func TestSmartlistSiblingReordering(t *testing.T) {
	for _, tc := range []struct {
		name, before string
		wantNext     string
		wantLink     string
	}{
		{"unchanged", "5", "Crates;House;", ""},
		{"append", "", "", "tail-head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := smartlistLibrary(t)
			if _, _, err := MovePlaylist(t.Context(), d, "2", "1", tc.before); err != nil {
				t.Fatal(err)
			}
			state := smartlistState(t, d)
			if row := state["before-house"]; row[1] != tc.wantNext || row[2] != tc.wantLink {
				t.Fatalf("preceding Smartlist=%v", row)
			}
			if row := state["under-deep"]; row[0] != "Crates;House;Deep;" {
				t.Fatalf("reordering changed descendant path: %v", row)
			}
		})
	}
}

func TestSmartlistOnlyFolderRequiresForce(t *testing.T) {
	d := smartlistLibrary(t)
	before := smartlistState(t, d)
	if _, err := DeletePlaylist(t.Context(), d, "3", false); err == nil {
		t.Fatal("nonempty Smartlist-only folder deleted without force")
	}
	if !reflect.DeepEqual(before, smartlistState(t, d)) {
		t.Fatal("refused removal changed Smartlists")
	}
	if _, err := DeletePlaylist(t.Context(), d, "3", true); err != nil {
		t.Fatal(err)
	}
	if _, exists := smartlistState(t, d)["under-deep"]; exists {
		t.Fatal("forced removal retained Smartlist child")
	}
}

func TestSmartlistTerminalAndInsertionLinks(t *testing.T) {
	t.Run("terminal deletion", func(t *testing.T) {
		d := smartlistLibrary(t)
		if _, err := d.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('before-last','before-last','Crates;','Crates;Household;','','last')`); err != nil {
			t.Fatal(err)
		}
		if _, err := DeletePlaylist(t.Context(), d, "6", true); err != nil {
			t.Fatal(err)
		}
		if row := smartlistState(t, d)["before-last"]; row[1] != "" || row[2] != "" {
			t.Fatalf("terminal predecessor=%v", row)
		}
	})
	t.Run("insert before chain target", func(t *testing.T) {
		d := smartlistLibrary(t)
		if _, _, err := MovePlaylist(t.Context(), d, "4", "1", "5"); err != nil {
			t.Fatal(err)
		}
		state := smartlistState(t, d)
		if row := state["tail"]; row[1] != "Crates;Sets;" || row[2] != "" {
			t.Fatalf("insertion terminal=%v", row)
		}
		if row := state["tail-head"]; row[1] != "" || row[2] != "tail" {
			t.Fatalf("insertion chain head=%v", row)
		}
		if row := state["destination-tail"]; row[0] != "Crates;Sets;" {
			t.Fatalf("moved descendant=%v", row)
		}
	})
}
