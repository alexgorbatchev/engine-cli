package details

import (
	"context"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"path/filepath"
	"testing"
)

func TestPlaylistUsesOriginIdentity(t *testing.T) {
	d, err := db.Open(filepath.Join(dbtest.Library(t, "destination"), "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO Track(id,title,originDatabaseUuid,originTrackId) VALUES(1,'Wrong local match','other',7),(42,'Imported track','source',1),(43,'Unrelated origin','third',1);
	INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(1,'Selection',0,0);
	INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0);`); err != nil {
		t.Fatal(err)
	}
	r, err := Read(context.Background(), d, "playlist", []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	tracks := r.Items[0].Data["tracks"].([]map[string]any)
	if len(tracks) != 1 || tracks[0]["id"] != int64(42) {
		t.Fatalf("wrong tracks: %+v", tracks)
	}
	if _, err := db.AddTrackToPlaylist(context.Background(), d, 1, 1); err != nil {
		t.Fatal(err)
	}
	r, err = Read(context.Background(), d, "playlist", []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items[0].Data["tracks"].([]map[string]any)) != 2 {
		t.Fatal(r)
	}
	if err := db.RemoveTrackFromPlaylist(context.Background(), d, 1, 42); err != nil {
		t.Fatal(err)
	}
	r, err = Read(context.Background(), d, "playlist", []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	tracks = r.Items[0].Data["tracks"].([]map[string]any)
	if len(tracks) != 1 || tracks[0]["id"] != int64(1) {
		t.Fatal(tracks)
	}
	if _, err := db.AddTrackToPlaylist(context.Background(), d, 1, 42); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTrack(context.Background(), d, 42); err != nil {
		t.Fatal(err)
	}
	r, err = Read(context.Background(), d, "playlist", []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	tracks = r.Items[0].Data["tracks"].([]map[string]any)
	if len(tracks) != 1 || tracks[0]["id"] != int64(1) {
		t.Fatalf("deleting imported track removed unrelated membership: %+v", tracks)
	}
}
