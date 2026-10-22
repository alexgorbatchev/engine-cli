package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestStagingAPIReadMediaAndRemoveUndo(t *testing.T) {
	s, lib := testDuplicateAPI(t)
	ctx := context.Background()
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("staging read/removal invoked desktop guard")
		return nil, nil
	}
	empty := requestAPI(t, s, http.MethodGet, "/v1/staging", "")
	if empty.Code != 200 || strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("empty %d %s", empty.Code, empty.Body)
	}
	store := staging.New(lib)
	item, err := store.Create(ctx, staging.Item{Kind: "track", Title: "Song", Artist: "Artist", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"audio.m4a", "cover.jpg", "invalid.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	item.AudioPath = item.ID + "/audio.m4a"
	item.ArtworkPath = item.ID + "/cover.jpg"
	if err := store.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"audio", "artwork"} {
		w := requestAPI(t, s, http.MethodGet, "/v1/staging/"+item.ID+"/"+route, "")
		if w.Code != 200 || w.Body.String() != "media" {
			t.Fatalf("media %s %d %s", route, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost/v1/staging/"+item.ID+"/audio", nil)
	r.Header.Set("Sec-Fetch-Dest", "audio")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("cross-origin media escaped origin check")
	}
	item.AudioPath = item.ID + "/invalid.txt"
	if err := store.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/staging/"+item.ID+"/audio", "")
	if w.Code != 415 {
		t.Fatalf("unsupported %d", w.Code)
	}
	runAPIJob(t, s, "staging.list", `{}`)
	runAPIJob(t, s, "staging.inspect", fmt.Sprintf(`{"id":%q}`, item.ID))
	duplicateJob := runAPIJob(t, s, "staging.duplicates", fmt.Sprintf(`{"id":%q}`, item.ID))
	if len(duplicateJob["result"].([]any)) != 2 {
		t.Fatalf("duplicate candidates %+v", duplicateJob)
	}
	result := runAPIJob(t, s, "staging.rm", fmt.Sprintf(`{"ids":[%q]}`, item.ID))
	if result["history_id"] == nil {
		t.Fatalf("no undo %+v", result)
	}
	assertHistoryScope(t, lib, result["history_id"].(string), history.ScopeFiles)
	if _, err := history.Undo(ctx, lib, result["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Inspect(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/staging/missing/audio", "")
	if w.Code != 404 {
		t.Fatalf("missing %d", w.Code)
	}
}

func TestStagingImportGuardAndCatalogEffects(t *testing.T) {
	s, _ := testAPI(t)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return nil, fmt.Errorf("desktop check unavailable")
	}
	op := s.operations["staging.import"]
	bound, err := op.bind(json.RawMessage(`{"ids":["00000000000000000000000000000000"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.execute(context.Background(), op, bound); err == nil || !strings.Contains(err.Error(), "desktop check") {
		t.Fatalf("guard %v", err)
	}
	for name, effect := range map[string]string{"staging.list": "read", "staging.inspect": "read", "staging.rm": "staging", "staging.import": "collection"} {
		if s.operations[name].Effect != effect {
			t.Errorf("%s effect %s", name, s.operations[name].Effect)
		}
	}
}

func TestStagingExecutionCreatesLibraryBeforeHistory(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "new-library")
	s, err := New(Config{LibraryDir: lib, CheckSystem: func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("staging creation invoked collection guard")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	op := defineEffect("staging.create-test", "staging", nil, func(ctx context.Context, _ emptyParams) (any, error) {
		return staging.New(lib).Create(ctx, staging.Item{Kind: "track", Status: "failed", Title: "Retry me"})
	})
	bound, err := op.bind(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result, id, err := s.execute(context.Background(), op, bound)
	if err != nil || id == "" {
		t.Fatalf("fresh staging operation: %+v %s %v", result, id, err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Database2", "m.db")); !os.IsNotExist(err) {
		t.Fatalf("staging created a collection database: %v", err)
	}
	if _, err := history.Undo(context.Background(), lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	items, err := staging.New(lib).List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("undo staging creation: %+v %v", items, err)
	}
}
