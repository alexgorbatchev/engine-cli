package api

import (
	"context"
	"encoding/json"
	"github.com/dj/engine-cli/internal/db"
	"os"
	"path/filepath"
	"testing"
)

func syncLibrary(t *testing.T, uuid string) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(path, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	schema, err := os.ReadFile("../transfer/testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Information(uuid,schemaVersionMajor,schemaVersionMinor,schemaVersionPatch) VALUES(?,3,0,2)", uuid); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestSyncAPIPreviewApplyAndDestinationUndo(t *testing.T) {
	s, configured := testAPI(t)
	source := syncLibrary(t, "source")
	dest := syncLibrary(t, "dest")
	d, err := db.Open(filepath.Join(source, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Set',0,0)"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	params := map[string]any{"source": source, "destination": dest, "scope": map[string]any{"playlist_ids": []int64{1}}, "dry_run": true}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	preview := runAPIJob(t, s, "library.sync", string(body))
	result := preview["result"].(map[string]any)
	for _, path := range []string{source, dest, configured} {
		if _, err = os.Stat(filepath.Join(path, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("preview wrote state at %s: %v", path, err)
		}
	}
	params["dry_run"] = false
	params["plan_token"] = result["plan_token"]
	body, err = json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	applied := runAPIJob(t, s, "library.sync", string(body))
	id := applied["history_id"].(string)
	body, err = json.Marshal(map[string]any{"library": dest, "id": id})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "library.undo", string(body))
	d, err = db.Open(filepath.Join(dest, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	playlists, err := db.GetAllPlaylists(context.Background(), d)
	if err != nil || len(playlists) != 0 {
		t.Fatalf("undo playlists: %v %v", playlists, err)
	}
	body, err = json.Marshal(map[string]string{"library": dest})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "library.history", string(body))
}
