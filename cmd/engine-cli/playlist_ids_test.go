package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
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

	var updated map[string]any
	if err := json.Unmarshal(run("track", "update", "3", "--title", "Renamed", "--json"), &updated); err != nil {
		t.Fatal(err)
	}
	check("track update", updated)
}
