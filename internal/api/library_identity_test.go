package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
)

type identifiedLibrary struct {
	Path         string `json:"path"`
	URL          string `json:"url"`
	Key          string `json:"key"`
	Instance     string `json:"instance"`
	EngineUUID   string `json:"engine_uuid"`
	Registration string `json:"registration"`
}

func libraryIdentities(t *testing.T, s http.Handler, path string) map[string]identifiedLibrary {
	t.Helper()
	w := requestAPI(t, s, http.MethodGet, path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var libraries []identifiedLibrary
	if err := json.Unmarshal(w.Body.Bytes(), &libraries); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]identifiedLibrary{}
	for _, lib := range libraries {
		byPath[lib.Path] = lib
	}
	return byPath
}

func setEngineUUID(t *testing.T, lib, uuid string) {
	t.Helper()
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("UPDATE Information SET uuid=?", uuid); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryListingsReportSyncIdentity(t *testing.T) {
	s, first := testAPI(t)
	first, err := filepath.EvalSymlinks(first)
	if err != nil {
		t.Fatal(err)
	}
	other, second := testAPI(t)
	other.Close()
	second, err = filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, second)); w.Code != http.StatusOK {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	// Both test libraries start from the same Engine database identity, like a hand copy.
	opened := libraryIdentities(t, s, "/v1/libraries")
	for _, path := range []string{first, second} {
		lib := opened[path]
		if lib.Registration != libraryops.RegistrationConflict || lib.Instance == "" || lib.Key == "" || lib.URL != "/v1/libraries/"+lib.Key {
			t.Fatalf("copied library %s: %+v", path, lib)
		}
	}

	setEngineUUID(t, second, "second-uuid")
	opened = libraryIdentities(t, s, "/v1/libraries")
	if got := opened[second]; got.Registration != libraryops.RegistrationUnregistered || got.Instance != "second-uuid" || got.EngineUUID != "second-uuid" {
		t.Fatalf("unregistered library: %+v", got)
	}
	runAPIJob(t, s, "library.register", "{}")
	opened = libraryIdentities(t, s, "/v1/libraries")
	registered := opened[first]
	if registered.Registration != libraryops.RegistrationRegistered || registered.Instance == registered.EngineUUID || registered.Instance == "" {
		t.Fatalf("registered library: %+v", registered)
	}

	missing := filepath.Join(t.TempDir(), "unplugged")
	if w := requestAPI(t, s, http.MethodPatch, "/v1/library-preferences", fmt.Sprintf(`{"path":%q,"hidden":true}`, missing)); w.Code != http.StatusOK {
		t.Fatalf("remember missing library: %d %s", w.Code, w.Body)
	}
	known := libraryIdentities(t, s, "/v1/library-preferences")
	for _, path := range []string{first, second} {
		if got, want := known[path], opened[path]; got.Instance != want.Instance || got.EngineUUID != want.EngineUUID || got.Registration != want.Registration {
			t.Fatalf("known library %s = %+v, opened as %+v", path, got, want)
		}
	}
	if got := known[missing]; got.Registration != libraryops.RegistrationUnavailable || got.Instance != "" {
		t.Fatalf("missing library: %+v", got)
	}

	w := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, first))
	if !strings.Contains(w.Body.String(), `"instance":"`+registered.Instance+`"`) {
		t.Fatalf("opening response lacks identity: %s", w.Body)
	}
}
