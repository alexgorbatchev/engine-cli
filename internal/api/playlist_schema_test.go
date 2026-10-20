package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestInspectPlaylistWithoutFolderColumn(t *testing.T) {
	s, library := testAPI(t)
	database, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(context.Background(), `
		INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) VALUES(1,1,'fixture');
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "playlist.inspect", `{"ids":[1]}`)
	result, ok := job["result"].(map[string]any)["items"].([]any)[0].(map[string]any)["data"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected result: %+v", job["result"])
	}
	tracks, ok := result["tracks"].([]any)
	if !ok || len(tracks) != 1 || result["trackCount"] != float64(1) {
		t.Fatalf("missing playlist tracks: %+v", result)
	}
	track, ok := tracks[0].(map[string]any)
	if !ok || track["id"] != float64(1) || track["title"] != "Curated title" {
		t.Fatalf("unexpected track: %+v", tracks[0])
	}
}

func TestCreatePlaylistWithoutFolderColumnAndUndo(t *testing.T) {
	s, library := testAPI(t)
	job := runAPIJob(t, s, "playlist.create", `{"name":"test","parent":"1"}`)
	database, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var id, parent int64
	var persisted, exported bool
	if err := database.QueryRow(`SELECT id,parentListId,isPersisted,isExplicitlyExported FROM Playlist WHERE title='test'`).Scan(&id, &parent, &persisted, &exported); err != nil {
		t.Fatal(err)
	}
	if parent != 1 || !persisted || !exported {
		t.Fatalf("id=%d parent=%d persisted=%v exported=%v", id, parent, persisted, exported)
	}
	runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM Playlist WHERE id=?", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("undo retained the created playlist")
	}
}
