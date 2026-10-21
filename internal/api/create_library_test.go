package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateLibraryAtExplicitPathAndUndo(t *testing.T) {
	s, original := testAPI(t)
	created := filepath.Join(t.TempDir(), "New library")
	job := runAPIJob(t, s, "library.create", fmt.Sprintf(`{"path":%q}`, created))
	if job["history_id"] == nil {
		t.Fatalf("creation is not undoable: %+v", job)
	}
	if _, err := os.Stat(filepath.Join(created, "Database2", "m.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(original, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("creation modified selected library: %v", err)
	}
	duplicate := requestAPI(t, s, http.MethodPost, "/v1/operations/library.create", fmt.Sprintf(`{"path":%q}`, created))
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("existing library accepted: %d %s", duplicate.Code, duplicate.Body)
	}
	runAPIJob(t, s, "library.undo", fmt.Sprintf(`{"library":%q,"id":%q}`, created, job["history_id"]))
	if _, err := os.Stat(filepath.Join(created, "Database2", "m.db")); !os.IsNotExist(err) {
		t.Fatalf("undo retained created database: %v", err)
	}
}

func TestCreateLibraryRejectsFilePathsBeforeStartingJobs(t *testing.T) {
	s, _ := testAPI(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("set list"), 0600); err != nil {
		t.Fatal(err)
	}
	response := requestAPI(t, s, http.MethodPost, "/v1/operations/library.create", fmt.Sprintf(`{"path":%q}`, file))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("library creation started inside a file: %d %s", response.Code, response.Body)
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "set list" {
		t.Fatalf("rejected creation changed the file: %q %v", content, err)
	}
}
