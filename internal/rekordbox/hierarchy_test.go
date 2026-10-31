package rekordbox

import (
	"reflect"
	"testing"

	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

func TestSourceHierarchyPreservesFoldersOrderAndOriginalSelectors(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="0" Location="file:///a.wav"/><TRACK Location="file:///b.wav"/></COLLECTION><PLAYLISTS><NODE Type="0" Name="ROOT"><NODE Type="0" Name="Empty folder"/><NODE Type="1" Name="Empty playlist"/><NODE Type="0" Name="Parent"><NODE Type="1" Name=" Ordered " KeyType="1"><TRACK Key="file:///b.wav"/><TRACK Key="file:///a.wav"/><TRACK Key="file:///b.wav"/><TRACK Key="file:///a.wav"/></NODE></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := sourceHierarchy(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []Playlist{
		{Name: "Empty folder", Folder: true, Tracks: []string{}, Children: []Playlist{}},
		{Name: "Empty playlist", Tracks: []string{}, Children: []Playlist{}},
		{Name: "Parent", Folder: true, Tracks: []string{}, Children: []Playlist{
			{Name: "Ordered", Tracks: []string{"ordinal:2", "id:0", "ordinal:2", "id:0"}, Children: []Playlist{}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source hierarchy lost identity, repeated membership, or order: %+v", got)
	}
	got[2].Children[0].Tracks[0] = "changed"
	if src.library.Playlists[2].SubPlaylists[0].Songs[0] != src.library.Songs[1].SongID {
		t.Fatal("review retained mutable conversion-owned memberships")
	}
}

func TestSourceHierarchyRejectsUnrepresentableTrees(t *testing.T) {
	for name, input := range map[string]struct {
		nodes []rbxml.Node
		lists []lib.Playlist
	}{
		"missing conversion": {nodes: []rbxml.Node{{Name: "Missing"}}},
		"empty name":         {nodes: []rbxml.Node{{Name: " "}}, lists: []lib.Playlist{{}}},
		"unknown track":      {nodes: []rbxml.Node{{Name: "Missing track"}}, lists: []lib.Playlist{{Songs: []int{42}}}},
		"missing child":      {nodes: []rbxml.Node{{Name: "Parent", Nodes: new([]rbxml.Node{{Name: "Child"}})}}, lists: []lib.Playlist{{}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := hierarchy(input.nodes, input.lists, nil)
			if err == nil || got != nil {
				t.Fatalf("unrepresentable source produced a partial hierarchy: %+v, %v", got, err)
			}
		})
	}
	got, err := sourceHierarchy(source{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("collection-only source: %+v, %v", got, err)
	}
}
