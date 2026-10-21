package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestArtworkCacheRevalidatesContent(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var original bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO AlbumArt(id,albumArt) VALUES(1,?); UPDATE Track SET albumArtId=1 WHERE id=1", original.Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "?size=full", "?version=arbitrary"} {
		t.Run(suffix, func(t *testing.T) {
			path := "/v1/tracks/1/artwork" + suffix
			first := requestAPI(t, s, http.MethodGet, path, "")
			etag := first.Header().Get("ETag")
			if first.Code != http.StatusOK || etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
				t.Fatalf("initial status=%d headers=%v", first.Code, first.Header())
			}
			r := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
			r.Header.Set("If-None-Match", etag)
			cached := httptest.NewRecorder()
			s.ServeHTTP(cached, r)
			if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
				t.Fatalf("cached status=%d body=%q", cached.Code, cached.Body.String())
			}
			img.Set(0, 0, color.RGBA{R: 255, A: 255})
			var changed bytes.Buffer
			if err := png.Encode(&changed, img); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec("UPDATE AlbumArt SET albumArt=? WHERE id=1", changed.Bytes()); err != nil {
				t.Fatal(err)
			}
			replaced := httptest.NewRecorder()
			s.ServeHTTP(replaced, r)
			if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") == etag || !bytes.Equal(replaced.Body.Bytes(), changed.Bytes()) {
				t.Fatalf("replacement status=%d headers=%v", replaced.Code, replaced.Header())
			}
			if _, err := d.Exec("UPDATE AlbumArt SET albumArt=? WHERE id=1", original.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := d.Exec("DELETE FROM AlbumArt"); err != nil {
		t.Fatal(err)
	}
	missing := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
	if missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" || missing.Header().Get("ETag") != "" {
		t.Fatalf("missing status=%d headers=%v", missing.Code, missing.Header())
	}
}

func TestStagedArtworkCacheRevalidatesContent(t *testing.T) {
	s, lib := testAPI(t)
	store := staging.New(lib)
	item, err := store.Create(context.Background(), staging.Item{Kind: "track", Title: "Cover", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.ArtworkPath = item.ID + "/cover.png"
	if err := store.Update(context.Background(), *item); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	url := "/v1/staging/" + item.ID + "/artwork"
	first := requestAPI(t, s, http.MethodGet, url, "")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("initial status=%d headers=%v", first.Code, first.Header())
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost"+url, nil)
	r.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	s.ServeHTTP(cached, r)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("cached status=%d body=%q", cached.Code, cached.Body.String())
	}
	if err := os.WriteFile(path, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	replaced := httptest.NewRecorder()
	s.ServeHTTP(replaced, r)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") == etag || replaced.Body.String() != "replaced" {
		t.Fatalf("replacement status=%d headers=%v body=%q", replaced.Code, replaced.Header(), replaced.Body.String())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing := requestAPI(t, s, http.MethodGet, url, "")
	if missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing status=%d headers=%v", missing.Code, missing.Header())
	}
}
