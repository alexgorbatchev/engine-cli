package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestAudioCacheRevalidatesCollectionAndStagedFiles(t *testing.T) {
	s, lib := testAPI(t)
	store := staging.New(lib)
	item, err := store.Create(context.Background(), staging.Item{Kind: "track", Title: "Cache test", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/audio.mp3"
	if err := store.Update(context.Background(), *item); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, route, path string }{
		{"collection", "/v1/tracks/1/audio", filepath.Join(lib, "song.mp3")},
		{"staged", "/v1/staging/" + item.ID + "/audio", filepath.Join(dir, "audio.mp3")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(tt.path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			modified := time.Unix(1600000000, 0)
			if err := os.Chtimes(tt.path, modified, modified); err != nil {
				t.Fatal(err)
			}
			head := requestAPI(t, s, http.MethodHead, tt.route, "")
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "8" || head.Header().Get("Cache-Control") != "private, no-cache" {
				t.Fatalf("HEAD: status=%d headers=%v body=%q", head.Code, head.Header(), head.Body.String())
			}
			validator := head.Header().Get("Last-Modified")
			if validator != modified.UTC().Format(http.TimeFormat) {
				t.Fatalf("Last-Modified=%q", validator)
			}
			r := httptest.NewRequest(http.MethodGet, "http://localhost"+tt.route, nil)
			r.Header.Set("If-Modified-Since", validator)
			unchanged := httptest.NewRecorder()
			s.ServeHTTP(unchanged, r)
			if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 {
				t.Fatalf("unchanged status=%d body=%q", unchanged.Code, unchanged.Body.String())
			}
			if err := os.WriteFile(tt.path, []byte("replaced"), 0600); err != nil {
				t.Fatal(err)
			}
			modified = modified.Add(time.Second)
			if err := os.Chtimes(tt.path, modified, modified); err != nil {
				t.Fatal(err)
			}
			replaced := httptest.NewRecorder()
			s.ServeHTTP(replaced, r)
			if replaced.Code != http.StatusOK || replaced.Body.String() != "replaced" || replaced.Header().Get("Last-Modified") == validator {
				t.Fatalf("replacement status=%d headers=%v body=%q", replaced.Code, replaced.Header(), replaced.Body.String())
			}
			if err := os.Remove(tt.path); err != nil {
				t.Fatal(err)
			}
			missing := requestAPI(t, s, http.MethodHead, tt.route, "")
			if missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("missing status=%d headers=%v", missing.Code, missing.Header())
			}
		})
	}
	metadata := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if metadata.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("metadata caching=%q", metadata.Header().Get("Cache-Control"))
	}
}
