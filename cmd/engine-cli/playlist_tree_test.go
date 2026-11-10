package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func execTreeFixture(t *testing.T, lib, statements string) {
	t.Helper()
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(statements); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPlaylistTreeCommand(t *testing.T) {
	lib := createTestLibrary(t)
	execTreeFixture(t, lib, `DELETE FROM Playlist; INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES (1,'Crates',0,0);
		INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
		('inside','Inside','Crates;','','','{"rules":[],"rv":2}'),('lost','Lost','Gone;','','','{}')`)
	for _, tc := range []struct {
		agent string
		want  []string
	}{
		{"0", []string{"Collection\n└── Crates [id:1 tracks:0]\n    └── Inside [smartlist:inside]\n", `Not shown: smart playlist "Lost" has damaged saved links [parent_path_unresolved].`}},
		{"1", []string{"- Collection\n  - Crates [id:1 tracks:0]\n    - Inside [smartlist:inside]\n", "unplaced_smartlist: uuid=lost code=parent_path_unresolved\n"}},
	} {
		t.Setenv("AGENT", tc.agent)
		out, err := executeCommand("-l", lib, "playlist", "tree")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Fatalf("AGENT=%s output missing %q:\n%s", tc.agent, want, out)
			}
		}
	}
}

func TestPlaylistTreeCommandEmptyAndFailure(t *testing.T) {
	lib := createTestLibrary(t)
	execTreeFixture(t, lib, `DELETE FROM Playlist; DELETE FROM Smartlist`)
	for agent, want := range map[string]string{"0": "No playlists found in database.\n", "1": "status: empty\n"} {
		t.Setenv("AGENT", agent)
		out, err := executeCommand("-l", lib, "playlist", "tree")
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("AGENT=%s output=%q err=%v", agent, out, err)
		}
	}
	execTreeFixture(t, lib, `DROP TABLE PlaylistEntity`)
	if out, err := executeCommand("-l", lib, "playlist", "tree"); err == nil {
		t.Fatalf("unreadable memberships reported as a tree: %q", out)
	}
	if out, err := executeCommand("-l", filepath.Join(lib, "missing"), "playlist", "tree"); err == nil {
		t.Fatalf("missing library reported as a tree: %q", out)
	}
}
