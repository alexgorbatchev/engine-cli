package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestLibraryTrackListIncludesTracksOutsidePlaylists(t *testing.T) {
	s, lib := testAPI(t)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO Track(id,title,path) VALUES(2,'Outside every playlist','outside.flac')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "track.list", "{}")
	tracks, ok := job["result"].([]any)
	if !ok || len(tracks) != 2 {
		t.Fatalf("track list = %#v", job["result"])
	}
	track := tracks[1].(map[string]any)
	if track["title"] != "Outside every playlist" || track["artist"] != nil {
		t.Fatalf("track = %#v", track)
	}
}

func TestTrackMetadataAndAudio(t *testing.T) {
	s, lib := testAPI(t)
	data := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, method, path, byteRange string
		status                        int
		body, contentType             string
	}{
		{"whole audio", http.MethodGet, "/v1/tracks/1/audio", "", http.StatusOK, string(data), "audio/mpeg"},
		{"seek", http.MethodGet, "/v1/tracks/1/audio", "bytes=2-5", http.StatusPartialContent, "2345", "audio/mpeg"},
		{"head", http.MethodHead, "/v1/tracks/1/audio", "", http.StatusOK, "", "audio/mpeg"},
		{"unknown track", http.MethodGet, "/v1/tracks/99/audio", "", http.StatusNotFound, "", "application/json"},
		{"invalid ID", http.MethodGet, "/v1/tracks/invalid/audio", "", http.StatusBadRequest, "", "application/json"},
		{"nonpositive ID", http.MethodGet, "/v1/tracks/0/audio", "", http.StatusBadRequest, "", "application/json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil)
			r.Host = "localhost"
			r.Header.Set("Range", tt.byteRange)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tt.status || w.Header().Get("Content-Type") != tt.contentType {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
			}
			if tt.status < 400 && w.Body.String() != tt.body {
				t.Fatalf("audio = %q, want %q", w.Body.String(), tt.body)
			}
			if tt.byteRange != "" && w.Header().Get("Content-Range") != "bytes 2-5/10" {
				t.Fatalf("range=%q", w.Header().Get("Content-Range"))
			}
		})
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	var track map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &track); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || track["title"] != "Curated title" || track["album"] != nil {
		t.Fatalf("metadata=%v status=%d", track, w.Code)
	}
	if err := os.Remove(filepath.Join(lib, "song.mp3")); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/audio", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing audio status=%d", w.Code)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, path := range []string{"", "folder", "page.html"} {
		if _, err := database.ExecContext(context.Background(), "UPDATE Track SET path=? WHERE id=1", path); err != nil {
			t.Fatal(err)
		}
		w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/audio", "")
		if w.Code < 400 {
			t.Fatalf("non-audio path %q status=%d", path, w.Code)
		}
	}
}

func TestAudioRejectsBrowserMediaWithoutAnAllowedOrigin(t *testing.T) {
	s, _ := testAPI(t)
	for _, origin := range []string{"", "https://example.com"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/tracks/1/audio", nil)
		r.Host = "localhost"
		r.Header.Set("Sec-Fetch-Dest", "audio")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("origin %q: status=%d", origin, w.Code)
		}
	}
}
