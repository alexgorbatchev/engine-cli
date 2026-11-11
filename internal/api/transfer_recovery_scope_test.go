package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func pendingSyncRepair(t *testing.T, lib string) string {
	t.Helper()
	path := filepath.Join(lib, ".engine-cli", "smartlist-repair", "pending.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("incomplete interrupted publication"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncAPIIgnoresUnrelatedConfiguredRecovery(t *testing.T) {
	s, configured := testAPI(t)
	s.config.CheckSystem = desktopClosed
	s.config.NoBackup = false
	pending := pendingSyncRepair(t, configured)
	source, destination := dbtest.Library(t, "source"), dbtest.Library(t, "destination")
	d, err := db.Open(filepath.Join(source, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close sync source fixture: %v", err)
		}
	})
	if _, err := d.Exec(`INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Set',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"source": source, "destination": destination, "scope": map[string]bool{"root": true}}
	applied := runAPIJob(t, s, "library.sync", string(mustJSON(t, params)))
	backups, err := os.ReadDir(filepath.Join(destination, ".engine-cli", "backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("destination backup missing: %v %v", backups, err)
	}
	id, ok := applied["history_id"].(string)
	if !ok || id == "" {
		t.Fatalf("missing destination Undo: %+v", applied)
	}
	runAPIJob(t, s, "library.undo", string(mustJSON(t, map[string]string{"library": destination, "id": id})))
	if data, err := os.ReadFile(pending); err != nil || string(data) != "incomplete interrupted publication" {
		t.Fatalf("unrelated recovery changed: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(configured, ".engine-cli"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "smartlist-repair" {
		t.Fatalf("unrelated library enrolled: %v %v", entries, err)
	}
}

func TestSyncAPIRecoveryUsesExplicitEndpoints(t *testing.T) {
	for _, endpoint := range []string{"source", "destination"} {
		for _, scoped := range []bool{false, true} {
			t.Run(endpoint+map[bool]string{false: "/global", true: "/scoped"}[scoped], func(t *testing.T) {
				s, _ := testAPI(t)
				s.config.CheckSystem = desktopClosed
				source, destination := dbtest.Library(t, "source"), dbtest.Library(t, "destination")
				handler := http.Handler(s)
				if scoped {
					response := requestAPI(t, s, http.MethodPost, "/v1/libraries", string(mustJSON(t, map[string]any{"paths": []string{source}})))
					var libraries []openedLibrary
					if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &libraries) != nil || len(libraries) != 1 {
						t.Fatalf("open: %d %s", response.Code, response.Body)
					}
					prefix := libraries[0].URL
					handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						r.URL.Path = prefix + strings.TrimPrefix(r.URL.Path, "/v1")
						s.ServeHTTP(w, r)
					})
				}
				blocked := source
				if endpoint == "destination" {
					blocked = destination
				}
				pending := pendingSyncRepair(t, blocked)
				params := map[string]any{"source": source, "destination": destination, "scope": map[string]bool{"root": true}}
				response := requestAPI(t, handler, http.MethodPost, "/v1/operations/library.sync", string(mustJSON(t, params)))
				var accepted Job
				if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &accepted) != nil {
					t.Fatalf("submit: %d %s", response.Code, response.Body)
				}
				job := awaitAPIJob(t, handler, accepted.ID)
				problem, ok := job["error"].(map[string]any)
				if !ok || problem["code"] != "smartlist_repair_pending" {
					t.Fatalf("actual recovery ignored: %+v", job)
				}
				if data, err := os.ReadFile(pending); err != nil || string(data) != "incomplete interrupted publication" {
					t.Fatalf("pending changed: %q %v", data, err)
				}
				if _, err := os.Stat(filepath.Join(destination, ".engine-cli", "backups")); !os.IsNotExist(err) {
					t.Fatalf("backup before recovery refusal: %v", err)
				}
			})
		}
	}
}

func TestSyncAPIStillRequiresDesktopClosed(t *testing.T) {
	s, _ := testAPI(t)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	source, destination := dbtest.Library(t, "source"), dbtest.Library(t, "destination")
	body := string(mustJSON(t, map[string]any{"source": source, "destination": destination, "scope": map[string]bool{"root": true}}))
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "library.sync", body))
	problem, ok := job["error"].(map[string]any)
	if !ok || problem["code"] != "engine_desktop_running" {
		t.Fatalf("Desktop protection bypassed: %+v", job)
	}
	for _, lib := range []string{source, destination} {
		if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("protected library enrolled: %v", err)
		}
	}
}
