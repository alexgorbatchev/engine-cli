//go:build pcm_native

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/pcm"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestNativePCMTrackStagingScopeAndRelease(t *testing.T) {
	s, lib := testAPI(t)
	path := filepath.Join(lib, "song.mp3")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=277:sample_rate=44100:duration=2.1", "-c:a", "pcm_s16le", "-f", "wav", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	store := staging.New(lib)
	item, err := store.Create(t.Context(), staging.Item{Kind: "track", Title: "Native", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(t.Context(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audio.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/audio.wav"
	if err := store.Update(t.Context(), *item); err != nil {
		t.Fatal(err)
	}
	_, other := testAPI(t)
	libraries, err := s.openLibraryPaths([]string{lib, other})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/v1/tracks/1/pcm", libraries[0].URL + "/tracks/1/pcm", "/v1/staging/" + item.ID + "/pcm"} {
		t.Run(target, func(t *testing.T) {
			created := requestAPI(t, s, http.MethodPost, target, `{"sample_rate":48000}`)
			var metadata pcm.Metadata
			if err := json.Unmarshal(created.Body.Bytes(), &metadata); err != nil {
				t.Fatal(err)
			}
			if created.Code != 201 || metadata.LengthFrames == nil || *metadata.LengthFrames != 100800 {
				t.Fatalf("created=%d %s", created.Code, created.Body)
			}
			location := created.Header().Get("Location")
			if strings.Contains(target, "/libraries/") && !strings.HasPrefix(location, libraries[0].URL+"/pcm/") {
				t.Fatalf("unscoped Location=%s", location)
			}
			window := requestAPI(t, s, http.MethodGet, location+"/windows?start_frame=100790&frame_count=20", "")
			if window.Code != 200 || window.Body.Len() != 10*pcm.BytesPerFrame || window.Header().Get("X-PCM-Length-Frames") != "100800" || window.Header().Get("X-PCM-Frame-Count") != "10" {
				t.Fatalf("window=%d %v %s", window.Code, window.Header(), window.Body)
			}
			for _, wrong := range []struct{ method, suffix string }{{http.MethodGet, ""}, {http.MethodGet, "/windows?start_frame=0&frame_count=1"}, {http.MethodDelete, ""}} {
				wrongLibrary := requestAPI(t, s, wrong.method, libraries[1].URL+"/pcm/"+metadata.ID+wrong.suffix, "")
				if wrongLibrary.Code != 404 {
					t.Fatalf("cross-library %s generation=%d", wrong.method, wrongLibrary.Code)
				}
			}
			if response := requestAPI(t, s, http.MethodDelete, location, ""); response.Code != 204 {
				t.Fatalf("release=%d %s", response.Code, response.Body)
			}
			if response := requestAPI(t, s, http.MethodGet, location, ""); response.Code != 404 {
				t.Fatalf("released metadata=%d", response.Code)
			}
		})
	}
	if !s.collection.TryAcquire(collectionCapacity) {
		t.Fatal("PCM retained the collection barrier")
	}
	s.collection.Release(collectionCapacity)
	request := httptest.NewRequest(http.MethodOptions, "http://localhost/v1/pcm/id", bytes.NewReader(nil))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "DELETE") {
		t.Fatal("release preflight denied")
	}
}
