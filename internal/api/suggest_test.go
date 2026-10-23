package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/llm"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": `{"items":[{"id":1,"title":"Song","artist":"Artist","reason":"Removed duplicated artist"}]}`}}}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	s.config.LLM = llm.Config{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: "test-key"}
	s.config.NoBackup = false
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return nil, errors.New("read-only suggestions must not check mutation permission")
	}
	for _, body := range []string{`{"tracks":["1"]}`, `{"playlists":["1"]}`} {
		job := runAPIJob(t, s, "track.suggest", body)
		item := job["result"].(map[string]any)["items"].([]any)[0].(map[string]any)
		if item["id"] != float64(1) || item["current"].(map[string]any)["title"] != "Artist - Song" || item["suggestion"].(map[string]any)["title"] != "Song" || job["history_id"] != nil || job["progress"].(map[string]any)["percent"] != float64(100) {
			t.Fatalf("job=%+v", job)
		}
	}
	for _, body := range []string{`{}`, `{"tracks":[]}`, `{"tracks":["0"]}`, `{"tracks":["Song"]}`, `{"tracks":["1"],"playlists":["1"]}`, `{"tracks":["1"],"provider":"openai"}`, `{"tracks":["1"],"base_url":"https://example.com"}`} {
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
	s.config.LLM = llm.Config{}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.suggest", `{"tracks":["1"]}`))
	if job["error"].(map[string]any)["code"] != "llm_configuration_invalid" || job["progress"].(map[string]any)["error_code"] != "llm_configuration_invalid" {
		t.Fatalf("job=%v", job)
	}
	catalog := requestAPI(t, s, http.MethodGet, "/v1/operations", "")
	var ops []operation
	if err := json.Unmarshal(catalog.Body.Bytes(), &ops); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Name == "track.suggest" && (op.Mutates || op.Effect != "read") {
			t.Fatalf("suggestions classified as mutation: %+v", op)
		}
	}
	if strings.Contains(catalog.Body.String(), "test-key") {
		t.Fatal("catalog exposed credentials")
	}
}
