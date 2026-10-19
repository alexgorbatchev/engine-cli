package api

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
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
