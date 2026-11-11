package playlist

import (
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestMovePlaylistAcrossSmartlistGaps(t *testing.T) {
	for _, tc := range []struct {
		name, before string
		want         []string
	}{
		{"before preceding smartlist", "smart:before-house", []string{"House", "before-house", "tail-head", "tail", "House2", "Household"}},
		{"original gap", "smart:tail-head", []string{"before-house", "House", "tail-head", "tail", "House2", "Household"}},
		{"split smartlist chain", "smart:tail", []string{"before-house", "tail-head", "House", "tail", "House2", "Household"}},
		{"cross smartlists without changing ordinary order", "5", []string{"before-house", "tail-head", "tail", "House", "House2", "Household"}},
		{"after last", "", []string{"before-house", "tail-head", "tail", "House2", "Household", "House"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := smartlistLibrary(t)
			before := smartlistState(t, d)
			if _, _, err := MovePlaylist(t.Context(), d, "2", "1", tc.before); err != nil {
				t.Fatal(err)
			}
			tree, err := db.ReadPlaylistTree(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, node := range tree.Nodes {
				if node.ID == 1 {
					for _, child := range node.Children {
						titles = append(titles, child.Title)
					}
				}
			}
			if !reflect.DeepEqual(titles, tc.want) || len(tree.Issues) != 0 {
				t.Fatalf("tree=%v issues=%v want=%v", titles, tree.Issues, tc.want)
			}
			after := smartlistState(t, d)
			for id, row := range before {
				if row[3] != after[id][3] {
					t.Fatalf("rules changed for %s", id)
				}
			}
		})
	}
}

func TestMovePlaylistInvalidSmartlistAnchorRollsBack(t *testing.T) {
	for _, anchor := range []string{"smart:", "smart:absent", "smart:destination-tail", "smart:under-house"} {
		t.Run(anchor, func(t *testing.T) {
			d := smartlistLibrary(t)
			before := smartlistState(t, d)
			playlists, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = MovePlaylist(t.Context(), d, "2", "1", anchor)
			if progress.ErrorCode(err) != "playlist_move_invalid" {
				t.Fatalf("error=%v", err)
			}
			after, err := db.GetAllPlaylists(t.Context(), d)
			if err != nil || !reflect.DeepEqual(playlists, after) || !reflect.DeepEqual(before, smartlistState(t, d)) {
				t.Fatalf("rejected move changed library: %v", err)
			}
		})
	}
}

func TestMovePlaylistSmartlistSpliceRollsBackAfterPlaylistFailure(t *testing.T) {
	d := smartlistLibrary(t)
	before := smartlistState(t, d)
	playlists, err := db.GetAllPlaylists(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_playlist_update BEFORE UPDATE ON Playlist BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MovePlaylist(t.Context(), d, "2", "1", "smart:tail"); err == nil {
		t.Fatal("expected rejected playlist write")
	}
	after, err := db.GetAllPlaylists(t.Context(), d)
	if err != nil || !reflect.DeepEqual(playlists, after) || !reflect.DeepEqual(before, smartlistState(t, d)) {
		t.Fatalf("failed splice changed library: %v", err)
	}
}

func TestMovePlaylistOriginalSmartlistGapIsUnchanged(t *testing.T) {
	d := smartlistLibrary(t)
	before := smartlistState(t, d)
	if _, err := d.Exec(`CREATE TRIGGER reject_playlist_update BEFORE UPDATE ON Playlist BEGIN SELECT RAISE(ABORT,'no-op wrote Playlist'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MovePlaylist(t.Context(), d, "2", "1", "smart:tail-head"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, smartlistState(t, d)) {
		t.Fatal("no-op changed Smartlists")
	}
}
