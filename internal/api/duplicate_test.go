package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func testDuplicateAPI(t *testing.T) (*Server, string) {
	t.Helper()
	initial, lib := testAPI(t)
	initial.Close()
	path := filepath.Join(lib, "Database2", "m.db")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("..", "duplicate", "testdata", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Information(uuid) VALUES('api-fixture'); INSERT INTO Track(title,artist,filename,path,length) VALUES('Song','Artist','one.mp3','one.mp3',180),('Song','Artist','two.mp3','two.mp3',180); UPDATE PerformanceData SET quickCues=x'01' WHERE trackId=1`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{LibraryDir: lib, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, lib
}

func TestAPIDuplicateReadOperations(t *testing.T) {
	s, _ := testDuplicateAPI(t)
	for _, body := range []string{`{}`, `{"type":"metadata"}`} {
		job := runAPIJob(t, s, "track.duplicate.list", body)
		groups, ok := job["result"].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("duplicate groups=%+v", job)
		}
		group, ok := groups[0].(map[string]any)
		if !ok {
			t.Fatalf("duplicate group=%+v", groups[0])
		}
		tracks, ok := group["tracks"].([]any)
		if !ok || len(tracks) != 2 {
			t.Fatalf("candidate tracks=%+v", group)
		}
		first, ok := tracks[0].(map[string]any)
		if !ok {
			t.Fatalf("candidate=%+v", tracks[0])
		}
		if playlists, ok := first["playlists"].([]any); !ok || len(playlists) != 0 {
			t.Fatalf("playlist memberships=%+v", first)
		}
	}
	job := runAPIJob(t, s, "track.duplicate.inspect", `{"ids":[1,2]}`)
	tracks, ok := job["result"].([]any)
	if !ok || len(tracks) != 2 {
		t.Fatalf("inspected tracks=%+v", job)
	}
	first, ok := tracks[0].(map[string]any)
	if !ok || first["has_cues"] != true || first["id"] != float64(1) {
		t.Fatalf("comparison data=%+v", tracks[0])
	}
	for _, tt := range []struct{ name, body string }{
		{"track.duplicate.list", `{"type":"invalid"}`},
		{"track.duplicate.inspect", `{"ids":[999]}`},
	} {
		id := submitAPIJob(t, s, tt.name, tt.body)
		if job := awaitAPIJob(t, s, id); job["state"] != "failed" {
			t.Fatalf("expected failed duplicate job=%+v", job)
		}
	}
}

func TestAPIAudioPreview(t *testing.T) {
	s, lib := testDuplicateAPI(t)
	job := runAPIJob(t, s, "track.duplicate.list", `{"type":"audio"}`)
	r, ok := job["result"].(map[string]any)
	if !ok || len(r["items"].([]any)) != 2 {
		t.Fatalf("audio preview: %v", job)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "fingerprints")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote cache: %v", err)
	}
}

func TestAPIDuplicateAndUpgradeParameterValidation(t *testing.T) {
	s, _ := testAPI(t)
	for _, tt := range []struct{ name, body string }{
		{"track.duplicate.inspect", `{"ids":[]}`},
		{"track.duplicate.inspect", `{"ids":[0]}`},
		{"track.duplicate.inspect", `{"ids":[-1]}`},
		{"track.upgrade", `{"identifier":"1","keep_timing":true,"clear_timing":true}`},
	} {
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+tt.name, tt.body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid params status=%d body=%s", w.Code, w.Body)
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["error"] == nil {
			t.Fatalf("missing validation error=%+v", response)
		}
	}
}
