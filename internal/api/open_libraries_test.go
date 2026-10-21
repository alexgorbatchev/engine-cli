package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestOpenExplicitLibraries(t *testing.T) {
	s, first := testAPI(t)
	initial := requestAPI(t, s, http.MethodGet, "/v1/libraries", "")
	var defaults []openedLibrary
	if err := json.Unmarshal(initial.Body.Bytes(), &defaults); err != nil || len(defaults) != 1 || !defaults[0].Default {
		t.Fatalf("configured library missing: %s %v", initial.Body, err)
	}
	other, second := testAPI(t)
	other.Close()
	second, err := filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(second, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE Track SET title='Second library' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"paths":[%q,%q,%q]}`, first, second, second)
	w := requestAPI(t, s, http.MethodPost, "/v1/libraries", body)
	if w.Code != http.StatusOK {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	var opened []struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[0].URL == opened[1].URL {
		t.Fatalf("opened: %+v", opened)
	}
	for _, lib := range opened {
		w = requestAPI(t, s, http.MethodGet, lib.URL+"/tracks/1", "")
		if w.Code != http.StatusOK {
			t.Fatalf("read: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "Second library") != (lib.Path == second) {
			t.Fatalf("wrong library: %s %s", lib.Path, w.Body)
		}
		w = requestAPI(t, s, http.MethodPost, lib.URL+"/operations/playlist.list", "{}")
		if w.Code != http.StatusAccepted {
			t.Fatalf("submit: %d %s", w.Code, w.Body)
		}
		location := w.Header().Get("Location")
		if len(location) <= len(lib.URL) || location[:len(lib.URL)] != lib.URL {
			t.Fatalf("unscoped location: %s", location)
		}
		if got := requestAPI(t, s, http.MethodGet, location, ""); got.Code != http.StatusOK {
			t.Fatalf("status: %d %s", got.Code, got.Body)
		}
		var accepted Job
		if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Path = lib.URL + strings.TrimPrefix(r.URL.Path, "/v1")
			s.ServeHTTP(w, r)
		})
		if job := awaitAPIJob(t, scoped, accepted.ID); job["state"] != "succeeded" {
			t.Fatalf("job: %+v", job)
		}
		events := requestAPI(t, s, http.MethodGet, location+"/events", "")
		if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"type":"complete"`) {
			t.Fatalf("events: %d %s", events.Code, events.Body)
		}
		for _, other := range opened {
			if other.URL != lib.URL {
				wrong := requestAPI(t, s, http.MethodGet, other.URL+"/jobs/"+accepted.ID, "")
				if wrong.Code != http.StatusNotFound {
					t.Fatalf("job leaked across libraries: %d", wrong.Code)
				}
			}
		}
		if nested := requestAPI(t, s, http.MethodPost, lib.URL+"/libraries", body); nested.Code != http.StatusNotFound {
			t.Fatalf("nested registry: %d", nested.Code)
		}
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/libraries", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var listed []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("list: %s %v", w.Body, err)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(path, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("opening modified library: %v", err)
		}
	}
}

func TestOpenedLibraryRetainsMutationGuard(t *testing.T) {
	_, lib := testAPI(t)
	s, err := New(Config{LibraryDir: t.TempDir(), LibraryPaths: []string{lib}, CheckSystem: func(context.Context, []string) (*systemcheck.Report, error) {
		return nil, errors.New("status unavailable")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var base string
	for _, opened := range s.libraries {
		base = opened.URL
	}
	w := requestAPI(t, s, http.MethodPost, base+"/operations/playlist.create", `{"name":"Must not be created"}`)
	var accepted Job
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = base + strings.TrimPrefix(r.URL.Path, "/v1")
		s.ServeHTTP(w, r)
	})
	job := awaitAPIJob(t, scoped, accepted.ID)
	if job["state"] != "failed" {
		t.Fatalf("guard bypassed: %+v", job)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("blocked mutation created history: %v", err)
	}
}

func TestOpenLibrariesValidation(t *testing.T) {
	s, lib := testAPI(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for _, body := range []string{`{}`, `null`, `{"paths":[]}`, `{"paths":[""]}`, `{"paths":[1]}`, `{"paths":["/"],"unknown":true}`, fmt.Sprintf(`{"paths":[%q,%q]}`, lib, missing)} {
		w := requestAPI(t, s, http.MethodPost, "/v1/libraries", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("created missing library: %v", err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/libraries/unknown/tracks/1", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown library: %d", w.Code)
	}
}

func TestStartupLibraryPaths(t *testing.T) {
	_, first := testAPI(t)
	_, second := testAPI(t)
	s, err := New(Config{LibraryDir: first, LibraryPaths: []string{first, second, second}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := requestAPI(t, s, http.MethodGet, "/v1/libraries", "")
	var opened []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil || len(opened) != 2 {
		t.Fatalf("startup libraries: %s %v", w.Body, err)
	}
	if bad, err := New(Config{LibraryDir: first, LibraryPaths: []string{filepath.Join(t.TempDir(), "absent")}}); err == nil {
		bad.Close()
		t.Fatal("missing startup library accepted")
	}
}

func TestOpeningAliasesAndInvalidDatabases(t *testing.T) {
	s, lib := testAPI(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(lib, alias); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q,%q]}`, lib, alias))
	var opened []openedLibrary
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil || len(opened) != 1 {
		t.Fatalf("aliases: %s %v", w.Body, err)
	}
	reopened := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, alias))
	if reopened.Body.String() != w.Body.String() {
		t.Fatalf("reopened: %s", reopened.Body)
	}
	invalid := t.TempDir()
	if err := os.Mkdir(filepath.Join(invalid, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(invalid, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE Unrelated(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, invalid))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-Engine database opened: %d %s", w.Code, w.Body)
	}
	wrongType := requestAPI(t, s, http.MethodPost, "/v1/libraries", "")
	if wrongType.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", wrongType.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/libraries", strings.NewReader(fmt.Sprintf(`{"paths":[%q]}`, lib)))
	r.Host = "localhost"
	r.Header.Set("Origin", "https://foreign.example")
	r.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, r)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", response.Code)
	}
	s.Close()
	w = requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, lib))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed server accepted a library: %d", w.Code)
	}
}

func TestOpeningLibraryLimitIsAtomic(t *testing.T) {
	s, fixture := testAPI(t)
	data, err := os.ReadFile(filepath.Join(fixture, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := make([]string, 65)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprint(i))
		if err := os.MkdirAll(filepath.Join(paths[i], "Database2"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths[i], "Database2", "m.db"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths[0] = fixture
	body, err := json.Marshal(map[string]any{"paths": paths})
	if err != nil {
		t.Fatal(err)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/libraries", string(body)); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized request: %d", w.Code)
	}
	body, err = json.Marshal(map[string]any{"paths": paths[:64]})
	if err != nil {
		t.Fatal(err)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/libraries", string(body)); w.Code != http.StatusOK {
		t.Fatalf("open limit: %d %s", w.Code, w.Body)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, paths[64])); w.Code != http.StatusBadRequest {
		t.Fatalf("limit exceeded: %d", w.Code)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/libraries", "")
	var opened []openedLibrary
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil || len(opened) != 64 {
		t.Fatalf("registry changed after rejection: %s %v", w.Body, err)
	}
}
