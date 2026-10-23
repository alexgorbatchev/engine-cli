package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLibraryPreferencesCrossOriginPatch(t *testing.T) {
	_, lib := testAPI(t)
	const origin = "http://localhost:3000"
	s, err := New(Config{LibraryDir: lib, StatePath: filepath.Join(t.TempDir(), "state.db"), AllowedOrigins: []string{origin}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	request := func(method, requestOrigin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1/v1/library-preferences", strings.NewReader(body))
		r.Header.Set("Origin", requestOrigin)
		if method == http.MethodOptions {
			r.Header.Set("Access-Control-Request-Method", http.MethodPatch)
			r.Header.Set("Access-Control-Request-Headers", "content-type")
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		s.ServeHTTP(response, r)
		return response
	}
	preflight := request(http.MethodOptions, origin, "")
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("preflight: status=%d headers=%v", preflight.Code, preflight.Header())
	}
	methods := strings.FieldsFunc(preflight.Header().Get("Access-Control-Allow-Methods"), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if !slices.Contains(methods, http.MethodPatch) {
		t.Fatalf("browser cannot PATCH library preferences: allowed methods=%v", methods)
	}
	response := request(http.MethodPatch, origin, fmt.Sprintf(`{"path":%q,"name":"My collection","hidden":true}`, lib))
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("cross-origin patch: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body)
	}
	for _, method := range []string{http.MethodOptions, http.MethodPatch} {
		denied := request(method, "https://untrusted.example", fmt.Sprintf(`{"path":%q,"name":"Foreign change","hidden":false}`, lib))
		if denied.Code != http.StatusForbidden || denied.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("foreign %s: status=%d headers=%v body=%s", method, denied.Code, denied.Header(), denied.Body)
		}
	}
	response = request(http.MethodGet, origin, "")
	var entries []struct {
		Path   string
		Name   string
		Hidden bool
	}
	if response.Code != http.StatusOK {
		t.Fatalf("preferences read: status=%d body=%s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != lib || entries[0].Name != "My collection" || !entries[0].Hidden {
		t.Fatalf("cross-origin patch was not saved: %s", response.Body)
	}
}

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
