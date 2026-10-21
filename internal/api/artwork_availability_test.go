package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/artworkcache"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestArtworkAvailabilityRejectsDanglingLinks(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET albumArtId=517 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	var metadata map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["hasArtwork"] != false {
		t.Fatalf("dangling artwork availability=%v", metadata["hasArtwork"])
	}
	job := runAPIJob(t, s, "track.list", `{}`)
	tracks := job["result"].([]any)
	if tracks[0].(map[string]any)["hasArtwork"] != false {
		t.Fatalf("list availability=%v", tracks)
	}
	if _, err := d.Exec("INSERT INTO AlbumArt(id,albumArt) VALUES(517,?)", []byte("inline artwork")); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["hasArtwork"] != true {
		t.Fatalf("inline artwork availability=%v", metadata["hasArtwork"])
	}
	if _, err := db.AddTrackToPlaylist(context.Background(), d, 1, 1); err != nil {
		t.Fatal(err)
	}
	playlist := runAPIJob(t, s, "playlist.inspect", `{"ids":[1]}`)
	items := playlist["result"].(map[string]any)["items"].([]any)
	data := items[0].(map[string]any)["data"].(map[string]any)
	if data["tracks"].([]any)[0].(map[string]any)["hasArtwork"] != true {
		t.Fatalf("playlist artwork availability=%v", data)
	}
}

func TestArtworkAvailabilityDoesNotCacheReadFailures(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	hash := []byte("0000000000000000000000000000000000000000")
	if _, err := d.Exec("UPDATE Track SET albumArtSourceHash=? WHERE id=1", hash); err != nil {
		t.Fatal(err)
	}
	paths := artworkcache.Paths(lib, hash)
	if err := os.MkdirAll(paths[0], 0700); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache failure status=%d headers=%v", w.Code, w.Header())
	}
	if _, err := d.Exec("DROP TABLE AlbumArt"); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("catalogue failure status=%d headers=%v", w.Code, w.Header())
	}
}
