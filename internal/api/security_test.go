package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPINewValidatesInputs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{
		{}, {LibraryDir: filepath.Join(t.TempDir(), "missing")}, {LibraryDir: file},
		{LibraryDir: t.TempDir(), AllowedOrigins: []string{"file:///local"}},
		{LibraryDir: t.TempDir(), AllowedOrigins: []string{"http://localhost:3000/path"}},
	} {
		if s, err := New(config); err == nil {
			s.Close()
			t.Fatalf("accepted invalid config %+v", config)
		}
	}
}

func TestAPIHostOriginAndPreflight(t *testing.T) {
	s, _ := testAPI(t)
	for _, host := range []string{"localhost", "127.0.0.1:8080", "[::1]:8080", "untrusted.example"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/operations", nil)
		r.Host = host
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := http.StatusOK
		if host == "untrusted.example" {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Fatalf("host=%s status=%d body=%s", host, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest(http.MethodOptions, "https://localhost/v1/operations", nil)
	r.Host = "localhost"
	r.Header.Set("Origin", "https://localhost")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://localhost" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Last-Event-ID") {
		t.Fatalf("preflight=%d headers=%v", w.Code, w.Header())
	}
}

func TestAPISubmitContentTypeAndSize(t *testing.T) {
	s, _ := testAPI(t)
	for _, tt := range []struct {
		contentType, body string
		want              int
	}{
		{"text/plain", `{}`, http.StatusUnsupportedMediaType},
		{"application/json", `{"unused":"` + strings.Repeat("x", maxRequestBytes) + `"}`, http.StatusBadRequest},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/operations/playlist.list", strings.NewReader(tt.body))
		r.Host = "localhost"
		r.Header.Set("Content-Type", tt.contentType)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("contentType=%s status=%d body=%s", tt.contentType, w.Code, w.Body)
		}
	}
}
