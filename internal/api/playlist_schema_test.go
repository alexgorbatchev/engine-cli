package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func TestInspectPlaylistWithoutFolderColumn(t *testing.T) {
	s, library := testAPI(t)
	database, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(context.Background(), `
		ALTER TABLE Playlist DROP COLUMN isFolder;
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
