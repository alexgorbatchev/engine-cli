package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestVerifyRawTargetDoesNotRequireCollection(t *testing.T) {
	for _, target := range []string{"https://example.test/audio.mp3", filepath.Join(t.TempDir(), "audio.mp3")} {
		s, err := New(Config{LibraryDir: t.TempDir(), ToolRunner: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "fetch-track" || !slices.Equal(args, []string{"--json", "verify", "--", target}) {
				t.Fatalf("native verification: %s %v", name, args)
			}
			return []byte(`{"status":"success"}`), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]string{"target": target})
		_, id, err := s.RunOperation(context.Background(), "track.verify", raw)
		s.Close()
		if err != nil || id != "" {
			t.Fatalf("raw verification: %s %v", id, err)
		}
	}
}

func TestMediaSelectionValidationAndFailureResults(t *testing.T) {
	s, _ := testAPI(t)
	for _, raw := range []string{`{}`, `{"paths":[" "]}`, `{"identifier":"1","path":"a.mp3"}`, `{"paths":` + string(mustJSON(t, make([]string, maxMediaBatch+1))) + `}`} {
		if _, err := s.operations["track.tags.inspect"].bind(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid selection %s", raw)
		}
	}
	result, _, err := s.RunOperation(context.Background(), "track.tags.inspect", json.RawMessage(`{"staging_ids":["missing","also-missing"]}`))
	if err == nil || result.(map[string]any)["status"] != "error" || len(result.(map[string]any)["results"].([]map[string]any)) != 2 {
		t.Fatalf("all-failed result: %#v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = s.tagSelection(ctx, mediaSelection{paths: []string{"a.mp3"}}, "inspect", acquire.TagOptions{})
	if !errors.Is(err, context.Canceled) || len(result.(map[string]any)["results"].([]map[string]any)) != 0 {
		t.Fatalf("cancelled batch: %#v %v", result, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRawCollectionAliasesRequireGuard(t *testing.T) {
	s, lib := testAPI(t)
	path := filepath.Join(lib, "song.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"original", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			alias := path
			if kind != "original" {
				alias = filepath.Join(t.TempDir(), "alias.mp3")
				var err error
				if kind == "symlink" {
					err = os.Symlink(path, alias)
				} else {
					err = os.Link(path, alias)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) { return nil, errors.New("guard reached") }
			_, id, err := s.RunOperation(context.Background(), "track.tags.update", mustJSON(t, map[string]any{"paths": []string{alias}, "title": "Edit"}))
			if err == nil || !strings.Contains(err.Error(), "guard reached") || id != "" {
				t.Fatalf("alias bypassed guard: %s %v", id, err)
			}
			if _, err := s.tagItem(context.Background(), "path", alias, "update", acquire.TagOptions{}); err == nil || !strings.Contains(err.Error(), "fresh safety check") {
				t.Fatalf("changed ownership bypassed recheck: %v", err)
			}
		})
	}
	other := filepath.Join(t.TempDir(), "other.mp3")
	if err := os.WriteFile(other, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if id, err := s.collectionPath(context.Background(), other); err != nil || id != "" {
		t.Fatalf("unmanaged ownership: %s %v", id, err)
	}
	loop := filepath.Join(t.TempDir(), "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mediaPathsEffect(context.Background(), []string{loop}); err == nil {
		t.Fatal("silenced filesystem error")
	}
}

func TestRawStagingPathUpdatesManifestAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	store := staging.New(lib)
	item, err := store.Create(ctx, staging.Item{Kind: "track", Status: "ready", Title: "Before"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/audio.mp3"
	if err := store.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("staging path invoked collection guard")
		return nil, nil
	}
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffmpeg" {
			return []byte("SHA256=same"), nil
		}
		if tool == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		if slices.Contains(args, "inspect") {
			return []byte(`{"status":"success","results":[{"status":"success","inspection":{"title":"After"}}]}`), nil
		}
		i := slices.Index(args, "--out-dir")
		output := filepath.Join(args[i+1], "updated.mp3")
		if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "output_file": output}}})
	}
	_, id, err := s.RunOperation(ctx, "track.tags.update", mustJSON(t, map[string]any{"path": path, "title": "After"}))
	if err != nil || id == "" {
		t.Fatalf("staged path: %s %v", id, err)
	}
	updated, err := store.Inspect(ctx, item.ID)
	if err != nil || updated.Title != "After" || updated.AudioPath == item.AudioPath {
		t.Fatalf("manifest not updated: %#v %v", updated, err)
	}
	if _, err := history.Undo(ctx, lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	updated, err = store.Inspect(ctx, item.ID)
	if err != nil || updated.Title != "Before" || updated.AudioPath != item.AudioPath {
		t.Fatalf("manifest undo: %#v %v", updated, err)
	}
}

func TestMediaCatalogReportsCompoundEffects(t *testing.T) {
	s, _ := testAPI(t)
	for _, name := range []string{"track.verify", "track.tags.update", "track.fetch", "mix.fetch"} {
		op := s.operations[name]
		if op.ParameterEffects["auto_install"] != "system" {
			t.Fatalf("%s hides installed-tool mutation", name)
		}
	}
	op := s.operations["track.verify"]
	for _, auto := range []bool{false, true} {
		bound, err := op.bind(mustJSON(t, map[string]any{"target": "https://example.test/audio", "auto_install": auto}))
		if err != nil || bound.effect != "read" {
			t.Fatalf("verify changed base effect: %#v %v", bound, err)
		}
	}
	retry := s.operations["staging.retry"]
	bound, err := retry.bind(json.RawMessage(`{"id":"saved"}`))
	if err != nil || bound.effect != "staging" || !slices.Equal(retry.AdditionalEffects, []string{"system"}) {
		t.Fatalf("retry effects %#v %#v %v", retry, bound, err)
	}
}

func TestRawCollectionMutationSnapshotsMetadataAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	s.config.NoBackup = false
	path := filepath.Join(lib, "song.mp3")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		switch tool {
		case "ffmpeg":
			return []byte("SHA256=same"), nil
		case "ffprobe":
			return []byte(`{"format":{"duration":"180","bit_rate":"320000","tags":{"title":"After","artist":"Artist"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
		default:
			i := slices.Index(args, "--out-dir")
			output := filepath.Join(args[i+1], "updated.mp3")
			if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
				t.Fatal(err)
			}
			return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "output_file": output}}})
		}
	}
	_, id, err := s.RunOperation(context.Background(), "track.tags.update", mustJSON(t, map[string]any{"path": path, "title": "After"}))
	if err != nil || id == "" {
		t.Fatalf("managed path: %s %v", id, err)
	}
	list, err := backup.ListBackups(lib, "")
	if err != nil || len(list) != 1 {
		t.Fatalf("missing safety snapshot: %#v %v", list, err)
	}
	read := func() string {
		value, err := s.database(context.Background(), func(d *sql.DB) (any, error) {
			var title string
			err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title)
			return title, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return value.(string)
	}
	if title := read(); title != "After" {
		t.Fatalf("collection metadata not updated: %s", title)
	}
	if _, err := history.Undo(context.Background(), lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if title := read(); title != "Curated title" {
		t.Fatalf("collection undo failed: %s", title)
	}
}

func TestTagBatchPreservesOrderAndPerItemFailures(t *testing.T) {
	s, lib := testAPI(t)
	s.config.ToolRunner = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "input_file": args[len(args)-1]}}})
	}
	for _, op := range s.mediaOperations() {
		s.operations[op.Name] = op
	}
	result, id, err := s.RunOperation(context.Background(), "track.tags.inspect", json.RawMessage(`{"identifiers":["1","999","1"]}`))
	if err == nil || id != "" {
		t.Fatalf("batch inspect: %s %v", id, err)
	}
	data, _ := json.Marshal(result)
	var batch struct {
		Results []struct {
			Identifier string          `json:"identifier"`
			Error      string          `json:"error"`
			Result     json.RawMessage `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 3 || batch.Results[0].Identifier != "1" || batch.Results[1].Identifier != "999" || batch.Results[1].Error == "" || batch.Results[2].Identifier != "1" || !strings.Contains(string(batch.Results[2].Result), filepath.Join(lib, "song.mp3")) {
		t.Fatalf("ordered results: %s", data)
	}
}

func TestRawTagBatchPartialSuccessAndUndo(t *testing.T) {
	lib := t.TempDir()
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.mp3"), filepath.Join(dir, "bad.mp3")
	for _, path := range []string{good, bad} {
		if err := os.WriteFile(path, []byte("original"), 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Config{LibraryDir: lib, ToolRunner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffmpeg" {
			return []byte("SHA256=identical-packets"), nil
		}
		if filepath.Base(args[len(args)-1]) == filepath.Base(bad) {
			return nil, errors.New("source unavailable")
		}
		index := slices.Index(args, "--out-dir")
		if index < 0 {
			t.Fatalf("no private output directory: %v", args)
		}
		output := filepath.Join(args[index+1], "tagged.mp3")
		if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "output_file": output}}})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, _ := json.Marshal(map[string]any{"paths": []string{good, bad}, "title": "Edited"})
	result, id, err := s.RunOperation(context.Background(), "track.tags.update", raw)
	if err == nil || id == "" {
		t.Fatalf("partial operation: %s %v", id, err)
	}
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), "source unavailable") {
		t.Fatalf("lost per-item error: %s", data)
	}
	for _, tc := range []struct{ path, want string }{{good, "tagged"}, {bad, "original"}} {
		got, err := os.ReadFile(tc.path)
		if err != nil || string(got) != tc.want {
			t.Fatalf("%s = %q, %v", tc.path, got, err)
		}
	}
	info, _ := os.Stat(good)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("changed permissions: %v", info.Mode())
	}
	if _, err := history.Undo(context.Background(), lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(good)
	if err != nil || string(got) != "original" {
		t.Fatalf("undo %q %v", got, err)
	}
}
