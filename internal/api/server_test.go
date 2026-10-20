package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func testAPI(t *testing.T) (*Server, string) {
	t.Helper()
	lib := t.TempDir()
	if err := os.Mkdir(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO Track(id,title,artist,path) VALUES(1,'Curated title','Artist','song.mp3'); INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Favorites',0)"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s, err := New(Config{LibraryDir: lib, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, lib
}

func requestAPI(t *testing.T, s http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "localhost"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func awaitAPIJob(t *testing.T, s http.Handler, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id, "")
		if w.Code != http.StatusOK {
			t.Fatalf("job status=%d body=%s", w.Code, w.Body)
		}
		var job map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		state, _ := job["state"].(string)
		if state != "queued" && state != "running" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete: %+v", job)
		}
		time.Sleep(time.Millisecond)
	}
}

func runAPIJob(t *testing.T, s http.Handler, operation, body string) map[string]any {
	t.Helper()
	w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+operation, body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%s submit status=%d body=%s", operation, w.Code, w.Body)
	}
	var accepted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, accepted.ID)
	if job["error"] != nil && job["error"] != "" {
		t.Fatalf("%s job failed: %+v", operation, job)
	}
	return job
}

func TestAPIOperationsAndReadJob(t *testing.T) {
	s, _ := testAPI(t)
	w := requestAPI(t, s, http.MethodGet, "/v1/operations", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "playlist.list") || !strings.Contains(w.Body.String(), "track.upgrade") {
		t.Fatalf("catalog status=%d body=%s", w.Code, w.Body)
	}
	w = requestAPI(t, s, http.MethodPost, "/v1/operations/playlist.list", "{}")
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body)
	}
	var accepted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.ID == "" {
		t.Fatal("missing job id")
	}
	job := awaitAPIJob(t, s, accepted.ID)
	encoded, err := json.Marshal(job["result"])
	if err != nil {
		t.Fatal(err)
	}
	if job["error"] != nil && job["error"] != "" || !strings.Contains(string(encoded), "Favorites") {
		t.Fatalf("read job=%+v", job)
	}
}

func TestAPIRejectsInvalidRequests(t *testing.T) {
	s, _ := testAPI(t)
	for _, tt := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/operations/playlist.list", `{"unexpected":true}`},
		{http.MethodPost, "/v1/operations/playlist.list", `{"broken"`},
		{http.MethodPost, "/v1/operations/not.an.operation", `{}`},
		{http.MethodGet, "/v1/jobs/missing", ""},
		{http.MethodPost, "/v1/operations/playlist.create", `{}`},
		{http.MethodPost, "/v1/operations/playlist.create", `{"name":null}`},
		{http.MethodPost, "/v1/operations/playlist.create", `{"name":" "}`},
		{http.MethodPost, "/v1/operations/playlist.create", `{"name":42}`},
		{http.MethodPost, "/v1/operations/playlist.create", `null`},
		{http.MethodPost, "/v1/operations/playlist.create", `[]`},
		{http.MethodPost, "/v1/operations/track.rm", `{"tracks":[]}`},
		{http.MethodPost, "/v1/operations/track.rm", `{"tracks":[""]}`},
		{http.MethodPost, "/v1/operations/playlist.list", `{} {}`},
	} {
		w := requestAPI(t, s, tt.method, tt.path, tt.body)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("request %s status=%d body=%s", tt.path, w.Code, w.Body)
		}
	}
}
