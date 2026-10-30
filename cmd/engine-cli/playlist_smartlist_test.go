package main

import (
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestPlaylistSmartlistCLIAndUndo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"rename", []string{"rename", "1", "Renamed"}},
		{"move", []string{"move", "1", "--to", "3"}},
		{"remove", []string{"rm", "1", "--force"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := createTestLibrary(t)
			d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec(`UPDATE Playlist SET nextListId=3 WHERE id=1;
			INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(3,'Destination',0,0);
			INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES('saved','Saved','EDM Folder;House Child;','','','opaque rules','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			read := func() string {
				t.Helper()
				var state string
				if err := d.QueryRow(`SELECT json_group_array(json_array(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime)) FROM (SELECT * FROM Smartlist ORDER BY listUuid)`).Scan(&state); err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := read()
			args := append([]string{"-l", root, "--no-backup", "playlist"}, tc.args...)
			if _, err := executeCommand(args...); err != nil {
				t.Fatal(err)
			}
			if read() == before {
				t.Fatal("CLI mutation left saved Smartlist paths unchanged")
			}
			if _, err := executeCommand("-l", root, "--no-backup", "library", "undo"); err != nil {
				t.Fatal(err)
			}
			if got := read(); got != before {
				t.Fatalf("Undo Smartlists=%s want=%s", got, before)
			}
		})
	}
}
