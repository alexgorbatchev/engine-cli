package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestClearArtworkAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET albumArtId=12,albumArt='image://planck/12',albumArtSourceHash='source' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "track.update", `{"identifier":"1","clear_artwork":true}`)
	var id any
	if err := d.QueryRow("SELECT albumArtId FROM Track WHERE id=1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != nil {
		t.Fatal("artwork remains linked", id)
	}
	runAPIJob(t, s, "library.undo", `{"id":"`+job["history_id"].(string)+`"}`)
	if err := d.QueryRow("SELECT albumArtId FROM Track WHERE id=1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != int64(12) {
		t.Fatal("artwork undo lost identity", id)
	}
}

func TestArtworkUploadValidation(t *testing.T) {
	for _, tt := range []struct{ name, metadata, field, body string }{
		{"trailing JSON", `{"title":"Cover"} {}`, "artwork", "image"},
		{"unknown metadata", `{"unknown":true}`, "artwork", "image"},
		{"missing image", `{}`, "metadata", "{}"},
		{"unexpected field", `{}`, "other", "image"},
		{"oversized image", `{}`, "artwork", strings.Repeat("x", 8<<20+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			if err := writer.WriteField("metadata", tt.metadata); err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteField(tt.field, tt.body); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPut, "/v1/tracks/1", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			if _, err := readArtworkUpload(request); err == nil {
				t.Fatal("invalid upload accepted")
			}
		})
	}
}

func TestMultipartMetadataEdit(t *testing.T) {
	s, _ := testAPI(t)
	s.config.ArtworkRunner = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		size := 256
		if slices.Contains(args, "crop=min(iw\\,ih):min(iw\\,ih),scale=1400:1400") {
			size = 1400
		}
		var out bytes.Buffer
		err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, size, size)), nil)
		return out.Bytes(), err
	}
	var body, cover bytes.Buffer
	if err := png.Encode(&cover, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("metadata", `{"title":"Uploaded cover"}`); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("artwork", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(cover.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "/v1/tracks/1", &body)
	r.Host = "localhost"
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var job Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	// The accepted upload follows the ordinary job/undo contract.
	completed := awaitAPIJob(t, s, job.ID)
	if completed["state"] != "succeeded" {
		t.Fatalf("%+v", completed)
	}
	if !reflect.DeepEqual(completed["result"], map[string]any{"track_id": float64(1)}) {
		t.Fatalf("upload result must report the edited track ID: %+v", completed["result"])
	}
	metadata := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if !bytes.Contains(metadata.Body.Bytes(), []byte(`"title":"Uploaded cover"`)) {
		t.Fatal(metadata.Body)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork?size=full", "")
	config, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || config.Width != 1400 || config.Height != 1400 {
		t.Fatalf("image=%+v error=%v response=%d", config, err, w.Code)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
	config, _, err = image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || config.Width != 256 || config.Height != 256 {
		t.Fatalf("thumbnail=%+v error=%v", config, err)
	}
	runAPIJob(t, s, "library.undo", `{"id":"`+completed["history_id"].(string)+`"}`)
	metadata = requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if bytes.Contains(metadata.Body.Bytes(), []byte(`"title":"Uploaded cover"`)) {
		t.Fatal("undo did not restore metadata")
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/artwork", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("undo retained uploaded artwork: %d", w.Code)
	}
}
