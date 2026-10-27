package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
)

// The fixture puts track 1 in playlist 2; this adds playlist 1 for it, an
// imported track 3 whose origin ID differs from its local ID in playlist 1, a
// decoy entity matching track 3's local ID, and track 4 in no playlist.
func TestCLITrackJSONReportsPlaylistIDs(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,path,originTrackId,originDatabaseUuid) VALUES(3,'Imported','imported.mp3',900,'imported'),(4,'Loose','loose.mp3',0,'');
	INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) VALUES(1,1,'fixture'),(1,900,'imported'),(2,3,'fixture')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	want := map[float64][]any{1: {float64(1), float64(2)}, 3: {float64(1)}, 4: {}}
	check := func(source string, track map[string]any) {
		t.Helper()
		id := track["id"].(float64)
		if got := track["playlistIds"]; !reflect.DeepEqual(got, want[id]) {
			t.Fatalf("%s: track %v playlistIds=%#v, want %v", source, id, got, want[id])
		}
	}
	run := func(args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		c := newRootCommand()
		c.SetOut(&out)
		c.SetErr(&bytes.Buffer{})
		c.SetArgs(append([]string{"-l", lib, "--no-backup"}, args...))
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}

	var tracks details.Report
	if err := json.Unmarshal(run("track", "inspect", "4", "3", "1", "--json"), &tracks); err != nil {
		t.Fatal(err)
	}
	for _, item := range tracks.Items {
		check("track inspect", item.Data)
	}

	var playlists struct {
		Items []struct {
			Data struct {
				Tracks []map[string]any `json:"tracks"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(run("playlist", "inspect", "1", "--json"), &playlists); err != nil {
		t.Fatal(err)
	}
	if len(playlists.Items) != 1 || len(playlists.Items[0].Data.Tracks) != 2 {
		t.Fatalf("playlist inspect=%+v", playlists)
	}
	for _, track := range playlists.Items[0].Data.Tracks {
		check("playlist inspect", track)
	}
}

// Edits report the changed track's ID rather than reading the track back.
func TestCLITrackUpdateReportsTrackID(t *testing.T) {
	lib := createTestLibrary(t)
	const undo = `operation_id: [0-9a-f]{32}\n`
	for _, tt := range []struct{ agent, flag, want string }{
		{"0", "--json", `\{"track_id":1\}\n`},
		{"1", "--json", `\{"track_id":1\}\n`},
		{"1", "--rating=4", `status: ok\ntrack_id: 1\n` + undo},
		{"0", "--rating=3", `Updated track \[id:1\]\.\n` + undo},
	} {
		t.Run(tt.agent+tt.flag, func(t *testing.T) {
			t.Setenv("AGENT", tt.agent)
			var out bytes.Buffer
			c := newRootCommand()
			c.SetOut(&out)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs([]string{"-l", lib, "--no-backup", "track", "update", "1", "--title", "Renamed", tt.flag})
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
			if !regexp.MustCompile(`\A` + tt.want + `\z`).MatchString(out.String()) {
				t.Fatalf("output=%q, want %q", out.String(), tt.want)
			}
		})
	}
}
