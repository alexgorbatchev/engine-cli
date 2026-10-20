package api

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestArtworkPreview(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO AlbumArt(id,albumArt) VALUES(1,?); UPDATE Track SET albumArtId=1 WHERE id=1", data.Bytes()); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), data.Bytes()) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, tt := range []struct {
		blob   []byte
		status int
	}{{nil, 404}, {[]byte("not an image"), 415}} {
		if _, err := d.Exec("UPDATE AlbumArt SET albumArt=? WHERE id=1", tt.blob); err != nil {
			t.Fatal(err)
		}
		w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
		if w.Code != tt.status {
			t.Fatalf("got %d want %d: %s", w.Code, tt.status, w.Body)
		}
	}
	for _, id := range []string{"missing", "999"} {
		w := requestAPI(t, s, http.MethodGet, "/v1/tracks/"+id+"/artwork", "")
		if w.Code < 400 {
			t.Fatalf("accepted %s", id)
		}
	}
}

func TestArtworkCachePreview(t *testing.T) {
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	hash := sha1.Sum(imageData.Bytes())
	hexHash := hex.EncodeToString(hash[:])
	urlHash := base64.RawURLEncoding.EncodeToString(hash[:])
	for _, tt := range []struct {
		name             string
		hash             any
		source, filename string
		linked           bool
	}{
		{"native binary hash", hash[:], "", urlHash, true},
		{"hex hash with URL cache", hexHash, "", urlHash, true},
		{"hex cache", hexHash, "", hexHash, true},
		{"binary hash with hex cache", hash[:], "", hexHash, true},
		{"source hash", nil, hexHash, urlHash, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if tt.linked {
				if _, err := d.Exec("INSERT INTO AlbumArt(id,hash) VALUES(1,?); UPDATE Track SET albumArtId=1 WHERE id=1", tt.hash); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.Exec("UPDATE Track SET albumArtSourceHash=? WHERE id=1", tt.source); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(lib, "Artwork"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lib, "Artwork", tt.filename+".jpg"), imageData.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
			if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), imageData.Bytes()) || w.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("cached artwork: status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestArtworkCacheMissingAndInvalid(t *testing.T) {
	for _, hash := range []string{"", "../outside", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "00112233445566778899aabbccddeeff00112233"} {
		t.Run(hash, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec("UPDATE Track SET albumArtSourceHash=? WHERE id=1", hash); err != nil {
				t.Fatal(err)
			}
			w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
			if w.Code != http.StatusNotFound {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}
