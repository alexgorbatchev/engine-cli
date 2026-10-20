package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverWithoutSelectedLibrary(t *testing.T) {
	initial, lib := testAPI(t)
	initial.Close()
	lib, err := filepath.EvalSymlinks(lib)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "not-selected")
	s, err := New(Config{LibraryDir: missing})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job := runAPIJob(t, s, "library.discover", fmt.Sprintf(`{"paths":[%q]}`, lib))
	result := job["result"].(map[string]any)
	libraries := result["libraries"].([]any)
	if len(libraries) != 1 || libraries[0].(map[string]any)["path"] != lib {
		t.Fatalf("discovery: %+v", job)
	}
	entry := libraries[0].(map[string]any)
	if entry["name"] == "" || entry["source"] != "folder" {
		t.Fatalf("missing picker fields: %+v", entry)
	}
	if job["history_id"] != nil {
		t.Fatal("read-only discovery created history")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("created selected library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("modified discovered library: %v", err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/operations", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
}
