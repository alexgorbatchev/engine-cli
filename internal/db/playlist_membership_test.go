package db

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlaylistMemberships(t *testing.T) {
	ctx := context.Background()
	d, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	// Track 5's origin identity differs from its local ID; the (3,5,'fixture')
	// entity matches only its local ID and must be ignored.
	if _, err := d.Exec(`INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Same',0),(2,'Same',1),(3,'Other',0);
	INSERT INTO Track(id,title) VALUES(1,'Two playlists'),(6,'None');
	INSERT INTO Track(id,title,originTrackId,originDatabaseUuid) VALUES(5,'Imported',900,'imported');
	INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(2,1,'fixture',0),(1,1,'fixture',0),(2,1,'fixture',0),(3,900,'imported',0),(3,5,'fixture',0)`); err != nil {
		t.Fatal(err)
	}
	many := make([]int64, 40000)
	for i := range many {
		many[i] = int64(i + 1)
	}
	want := map[int64][]PlaylistMembership{1: {{1, "Same"}, {2, "Same"}}, 5: {{3, "Other"}}}
	for _, tt := range []struct {
		name string
		ids  []int64
		want map[int64][]PlaylistMembership
	}{
		{"selected tracks", []int64{6, 5, 1, 999}, want},
		{"beyond SQLite variable limits", many, want},
		{"only listed tracks", []int64{5}, map[int64][]PlaylistMembership{5: {{3, "Other"}}}},
		{"no playlists", []int64{6}, map[int64][]PlaylistMembership{}},
		{"no tracks", nil, map[int64][]PlaylistMembership{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PlaylistMemberships(ctx, d, tt.ids)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("memberships=%v err=%v, want %v", got, err, tt.want)
			}
		})
	}
	if _, err := d.Exec("DROP TABLE PlaylistEntity"); err != nil {
		t.Fatal(err)
	}
	if _, err := PlaylistMemberships(ctx, d, []int64{1}); err == nil {
		t.Fatal("accepted unreadable memberships")
	}
}
