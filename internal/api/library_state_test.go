package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryPreferencesHTTPPersistence(t *testing.T) {
	_, lib := testAPI(t)
	cfg := Config{LibraryDir: lib, StatePath: filepath.Join(t.TempDir(), "state.db")}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	response := requestAPI(t, s, http.MethodPatch, "/v1/library-preferences", fmt.Sprintf(`{"path":%q,"name":"My collection","hidden":true}`, lib))
	if response.Code != 200 {
		t.Fatalf("patch: %d %s", response.Code, response.Body)
	}
	s.Close()
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	response = requestAPI(t, s, http.MethodGet, "/v1/library-preferences", "")
	var entries []struct {
		Path   string
		Name   string
		Hidden bool
	}
	if err = json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "My collection" || !entries[0].Hidden {
		t.Fatalf("lost state: %s", response.Body)
	}
	response = requestAPI(t, s, http.MethodGet, "/v1/libraries", "")
	if response.Code != 200 || response.Body.String() == "[]\n" {
		t.Fatal("hidden preferences removed CLI library knowledge")
	}
}

func TestStateFailuresAndUnsafePlacementAreReported(t *testing.T) {
	_, lib := testAPI(t)
	s, err := New(Config{LibraryDir: lib, StatePath: filepath.Join(lib, "state.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, _ := json.Marshal(map[string]any{"path": lib, "dry_run": true})
	if _, _, err = s.RunOperation(context.Background(), "library.delete", raw); err == nil {
		t.Fatal("deletion allowed its own state database")
	}
	response := requestAPI(t, s, http.MethodPatch, "/v1/library-preferences", `{"path":"relative","hidden":true}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid preference accepted: %d", response.Code)
	}
	response = requestAPI(t, s, http.MethodPatch, "/v1/library-preferences", fmt.Sprintf(`{"path":%q,"name":" "}`, lib))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid name accepted: %d", response.Code)
	}
	s.state.Close()
	response = requestAPI(t, s, http.MethodGet, "/v1/library-preferences", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("closed state read: %d", response.Code)
	}
	response = requestAPI(t, s, http.MethodPatch, "/v1/library-preferences", fmt.Sprintf(`{"path":%q,"hidden":true}`, lib))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("closed state write: %d", response.Code)
	}
}

func TestLibraryPreferencesRejectNonJSONRequests(t *testing.T) {
	s, lib := testAPI(t)
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		r := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1/v1/library-preferences", strings.NewReader(fmt.Sprintf(`{"path":%q,"hidden":true}`, lib)))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("content type %q: %d %s", contentType, w.Code, w.Body)
		}
	}
}
