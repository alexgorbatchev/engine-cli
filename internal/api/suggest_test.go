package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestSuggestionHTTPJob(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE Track SET title='Artist - Song' WHERE id=1; INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.0.0; exit; fi\ncat > /dev/null\nprintf '{\"harness\":\"codex\",\"output_kind\":\"json\",\"output\":{\"items\":[{\"id\":1,\"title\":\"Song\",\"artist\":\"Artist\",\"reason\":\"Removed duplicated artist\"}]}}'\n"
	if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.config.NoBackup = false
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return nil, errors.New("read-only suggestions must not check mutation permission")
	}
	for _, body := range []string{`{"tracks":["1"],"harness":"codex"}`, `{"playlists":["1"],"harness":"codex"}`} {
		job := runAPIJob(t, s, "track.suggest", body)
		item := job["result"].(map[string]any)["items"].([]any)[0].(map[string]any)
		if item["id"] != float64(1) || item["current"].(map[string]any)["title"] != "Artist - Song" || item["suggestion"].(map[string]any)["title"] != "Song" || job["history_id"] != nil || job["progress"].(map[string]any)["percent"] != float64(100) {
			t.Fatalf("job=%+v", job)
		}
	}
	for _, body := range []string{`{}`, `{"tracks":[]}`, `{"tracks":["0"]}`, `{"tracks":["Song"]}`, `{"tracks":["1"]}`, `{"tracks":["1"],"harness":"unknown"}`, `{"tracks":["1"],"harness":"codex","timeout":"0s"}`, `{"tracks":["1"],"playlists":["1"]}`, `{"tracks":["1"],"provider":"openai"}`, `{"tracks":["1"],"base_url":"https://example.com"}`} {
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.suggest", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s: %s", body, w.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("read-only operation wrote history or backups: %v", err)
	}
	d, err = db.Open(filepath.Join(lib, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Artist - Song" {
		t.Fatalf("changed title: %s %v", title, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.0.0; exit; fi\necho '{\"error\":{\"code\":\"harness_unavailable\",\"message\":\"Install Codex\"}}' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.suggest", `{"tracks":["1"],"harness":"codex"}`))
	if job["error"].(map[string]any)["code"] != "harness_unavailable" {
		t.Fatal(job)
	}
	catalog := requestAPI(t, s, http.MethodGet, "/v1/operations", "")
	var ops []operation
	if err := json.Unmarshal(catalog.Body.Bytes(), &ops); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Name == "track.suggest" {
			if len(op.Required) != 1 || op.Required[0] != "harness" {
				t.Fatalf("missing required harness: %+v", op)
			}
			if op.Mutates || op.Effect != "read" || op.ParameterEffects["auto_install"] != "system" {
				t.Fatalf("incorrect suggestion effects: %+v", op)
			}
			for _, name := range []string{"tracks", "playlists", "harness", "model", "effort", "thinking", "harness_config_mode", "harness_config_file", "harness_home_dir", "timeout", "auto_install"} {
				if op.Parameters[name] == "" {
					t.Fatalf("missing parameter %s: %+v", name, op)
				}
			}
		}
	}
	if strings.Contains(catalog.Body.String(), "test-key") {
		t.Fatal("catalog exposed credentials")
	}
}
