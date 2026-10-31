package suggest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

func TestSelection(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	d, err := db.Open(filepath.Join(lib, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec(`INSERT INTO Track(id,title,artist) VALUES(1,'Artist - Song','Artist'),(2,'Other',NULL);
		INSERT INTO Playlist(id,title,parentListId) VALUES(1,'First',0),(2,'Second',0),(3,'Empty',0);
		INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track;
		INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 2,originTrackId,originDatabaseUuid FROM Track WHERE id=2`)
	if err != nil {
		t.Fatal(err)
	}
	linkSuggestionMemberships(t, d)
	if _, err := identity.Register(ctx, d, lib, false); err != nil {
		t.Fatal(err)
	}
	entities, err := identity.List(ctx, d, lib)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for _, entity := range entities {
		refs[fmt.Sprintf("%s%d", entity.Kind, entity.ID)] = entity.Ref
	}
	for _, tc := range []struct {
		name      string
		selection Selection
		want      []int64
	}{
		{"tracks", Selection{Tracks: []string{"2", "1", "2"}}, []int64{2, 1}},
		{"playlists", Selection{Playlists: []string{"2", "1", "3"}}, []int64{2, 1}},
		{"empty playlist", Selection{Playlists: []string{"3"}}, nil},
		{"track references", Selection{Tracks: []string{refs["track2"], "1"}}, []int64{2, 1}},
		{"playlist references", Selection{Playlists: []string{refs["playlist1"]}}, []int64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := selectTracks(ctx, d, tc.selection)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Items) != len(tc.want) {
				t.Fatalf("items=%+v", r.Items)
			}
			for i, id := range tc.want {
				if r.Items[i].ID != id || r.Items[i].Ref != refs[fmt.Sprintf("track%d", id)] {
					t.Fatalf("items=%+v", r.Items)
				}
			}
		})
	}
	for _, selection := range []Selection{
		{}, {Tracks: []string{"1"}, Playlists: []string{"1"}},
		{Tracks: []string{"0"}}, {Tracks: []string{"-1"}}, {Tracks: []string{"Song"}},
		{Tracks: []string{"eng:wrong:playlist:id"}}, {Tracks: []string{"eng::track:id"}},
		{Tracks: []string{"eng:wrong:track:id:extra"}}, {Tracks: make([]string, details.MaxItems+1)},
		{Tracks: []string{"999"}}, {Playlists: []string{"999"}},
		{Tracks: []string{"eng:missing:track:id"}}, {Playlists: []string{"eng:missing:playlist:id"}},
	} {
		if _, err := selectTracks(ctx, d, selection); err == nil {
			t.Fatalf("accepted %+v", selection)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := selectTracks(cancelled, d, Selection{Tracks: []string{"1"}}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := d.Exec(`WITH RECURSIVE n(id) AS (SELECT 3 UNION ALL SELECT id+1 FROM n WHERE id<501)
		INSERT INTO Track(id,title) SELECT id,'Song' FROM n;
		INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track WHERE id>2`); err != nil {
		t.Fatal(err)
	}
	linkSuggestionMemberships(t, d)
	if r, err := selectTracks(ctx, d, Selection{Playlists: []string{"1"}}); err != nil || len(r.Items) != 501 {
		t.Fatalf("large playlist was truncated: %+v %v", r, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Selection{{Tracks: []string{"1"}}, {Playlists: []string{"1"}}} {
		if _, err := selectTracks(ctx, d, s); err == nil {
			t.Fatal("ignored database failure")
		}
	}
}
