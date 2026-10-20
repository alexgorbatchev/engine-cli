package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquisition"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestAcquisitionSelectionBoundToOriginalRequest(t *testing.T) {
	for _, kind := range []string{"track", "mix"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := testAPI(t)
			calls := 0
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
				t.Fatal("candidate discovery used collection guard")
				return nil, nil
			}
			s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
				calls++
				if tool != "fetch-"+kind || !slices.Contains(args, "--list-candidates") {
					t.Fatalf("unexpected acquisition %s %v", tool, args)
				}
				return []byte(`{"status":"candidates","candidates":[{"title":"Chosen","url":"https://example.test/allowed","webpage_url":"https://example.test/allowed"}]}`), nil
			}
			// Operations capture their process boundary at construction time.
			for _, op := range s.acquisitionOperations() {
				s.operations[op.Name] = op
			}
			result, historyID, err := s.RunOperation(context.Background(), kind+".candidates", json.RawMessage(`{"query":"Original","sources":"youtube"}`))
			if err != nil || historyID == "" {
				t.Fatalf("candidates: %v %s", err, historyID)
			}
			response := result.(map[string]any)
			token := response["selection_token"].(string)
			for _, change := range []map[string]any{
				{"query": "Changed"}, {"sources": "bandcamp"}, {"candidate_url": "https://example.test/unlisted"}, {"selection_token": "bad"},
			} {
				parameters := map[string]any{"query": "Original", "sources": "youtube", "selection_token": token, "candidate_url": "https://example.test/allowed"}
				for key, value := range change {
					parameters[key] = value
				}
				raw, _ := json.Marshal(parameters)
				if _, _, err := s.RunOperation(context.Background(), kind+".fetch", raw); err == nil {
					t.Fatalf("accepted mismatched selection %s", raw)
				}
			}
			if calls != 1 {
				t.Fatalf("mismatches invoked downloader: %d", calls)
			}
		})
	}
}

func TestAcquisitionImportGuardPrecedesToolsAndHistory(t *testing.T) {
	s, lib := testAPI(t)
	s.config.ToolRunner = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unsafe import invoked tool")
		return nil, nil
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return nil, errors.New("desktop unavailable")
	}
	for _, operation := range []string{"track.fetch", "mix.fetch"} {
		if _, id, err := s.RunOperation(context.Background(), operation, json.RawMessage(`{"query":"Song","import":true}`)); err == nil || !strings.Contains(err.Error(), "desktop unavailable") || id != "" {
			t.Fatalf("%s guard: %s %v", operation, id, err)
		}
		if _, _, err := s.RunOperation(context.Background(), operation, json.RawMessage(`{"query":"Song","to_playlist":"1"}`)); err == nil || !strings.Contains(err.Error(), "requires import") {
			t.Fatalf("%s orphan playlist: %v", operation, err)
		}
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("blocked import wrote sidecars: %v", err)
	}
}

func TestStagedTagDryRunPreservesFilesAndExplicitEmptyValues(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	store := staging.New(lib)
	item, err := store.Create(ctx, staging.Item{Kind: "track", Title: "Keep", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("unchanged audio"), 0600); err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/audio.mp3"
	if err := store.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("dry-run used guard")
		return nil, nil
	}
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool != "tag-track" || !slices.Contains(args, "--dry-run") {
			t.Fatalf("tag command %s %v", tool, args)
		}
		index := slices.Index(args, "--artist")
		if index < 0 || args[index+1] != "" || slices.Contains(args, "--album") {
			t.Fatalf("nullable overrides %v", args)
		}
		return []byte(`{"status":"preview","results":[{"status":"preview","dry_run":true}]}`), nil
	}
	for _, op := range s.mediaOperations() {
		s.operations[op.Name] = op
	}
	before := acquisitionTree(t, lib)
	_, id, err := s.RunOperation(ctx, "track.tags.update", json.RawMessage(fmt.Sprintf(`{"staging_id":%q,"artist":"","dry_run":true}`, item.ID)))
	if err != nil || id != "" {
		t.Fatalf("dry-run: %s %v", id, err)
	}
	if after := acquisitionTree(t, lib); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run changed library files\nbefore: %v\nafter: %v", before, after)
	}
}

func acquisitionTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		result[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAcquisitionToolsAndProviderDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, raw, tool, output string
		args                    []string
	}{
		{"tool.inspect", `{"tool":"fetch-track"}`, "fetch-track", "1.2.3\n", []string{"--version"}},
		{"tool.upgrade", `{"tool":"tag-track"}`, "tag-track", `{"status":"current"}`, []string{"--json", "upgrade"}},
		{"tool.dependencies.inspect", `{"tool":"fetch-mix"}`, "fetch-mix", `{"status":"success"}`, []string{"--json", "dependencies"}},
		{"tool.dependencies.install", `{"tool":"fetch-track","dependencies":["ffmpeg","deno"]}`, "fetch-track", `{"status":"success"}`, []string{"--json", "dependencies", "install", "ffmpeg", "deno"}},
		{"tool.dependencies.update", `{"tool":"tag-track","dependencies":["ffmpeg"]}`, "tag-track", `{"status":"success"}`, []string{"--json", "dependencies", "update", "ffmpeg"}},
		{"ai.list", `{}`, "fetch-mix", `{"providers":[]}`, []string{"--json", "ai", "list"}},
		{"ai.inspect", `{"provider":"openai"}`, "fetch-mix", `{"ID":"openai"}`, []string{"--json", "ai", "inspect", "openai"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, lib := testAPI(t)
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
				t.Fatal("system/read operation used collection guard")
				return nil, nil
			}
			s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
				if tc.name == "tool.inspect" && tool == tc.tool && slices.Equal(args, []string{"--json", "dependencies"}) {
					return []byte(`{"status":"ok","dependencies":[],"changed":[]}`), nil
				}
				if tool != tc.tool || !slices.Equal(args, tc.args) {
					t.Fatalf("dispatch %s %v", tool, args)
				}
				return []byte(tc.output), nil
			}
			for _, op := range s.toolOperations() {
				s.operations[op.Name] = op
			}
			result, id, err := s.RunOperation(context.Background(), tc.name, json.RawMessage(tc.raw))
			if err != nil || result == nil || id != "" {
				t.Fatalf("dispatch result %v %s %v", result, id, err)
			}
			if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("tool operation wrote library history: %v", err)
			}
		})
	}
}

func TestRunAcquisitionRejectsInvalidAndCancelledRequests(t *testing.T) {
	s, _ := testAPI(t)
	for _, tc := range []struct{ name, raw string }{
		{"missing", `{}`}, {"track.fetch", `{"query":"Song","unknown":true}`}, {"track.fetch", `{"query":"Song","sources":"invalid"}`},
		{"mix.inspect", `{"query":"Mix","extractor":"invalid"}`}, {"track.verify", `{}`}, {"track.verify", `{"identifier":"1","staging_id":"x"}`},
		{"tool.inspect", `{"tool":"invalid"}`}, {"ai.inspect", `{}`},
		{"track.artwork.embed", `{"staging_id":"missing","artwork":"cover.png","dry_run":true}`},
		{"track.artwork.embed", `{"staging_id":"missing","artwork":"cover.png","artist":"ignored"}`},
	} {
		if _, _, err := s.RunOperation(context.Background(), tc.name, json.RawMessage(tc.raw)); err == nil {
			t.Fatalf("accepted %s %s", tc.name, tc.raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.RunOperation(ctx, "ai.list", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestArtworkRejectsUnsupportedTagOptionsBeforeExecution(t *testing.T) {
	s, _ := testAPI(t)
	for _, field := range []string{`"dry_run":true`, `"artist":"ignored"`} {
		_, err := s.operations["track.artwork.embed"].bind(json.RawMessage(`{"staging_id":"missing","artwork":"cover.png",` + field + `}`))
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("unsupported artwork option was accepted: %s: %v", field, err)
		}
	}
}

func TestUpgradeRequiresBoundCandidateToken(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
	calls := 0
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(`{"status":"candidates","candidates":[{"title":"Chosen","webpage_url":"https://example.test/chosen"}]}`), nil
		}
		if tool != "fetch-track" || !slices.Contains(args, "--candidate-url") || !slices.Contains(args, "https://example.test/chosen") {
			t.Fatalf("selected upgrade %s %v", tool, args)
		}
		return nil, errors.New("selected acquisition reached")
	}
	for _, op := range s.trackOperations() {
		s.operations[op.Name] = op
	}
	result, _, err := s.RunOperation(context.Background(), "track.candidates", json.RawMessage(`{"query":"Chosen search"}`))
	if err != nil {
		t.Fatal(err)
	}
	token := result.(map[string]any)["selection_token"].(string)
	for _, tc := range []struct{ query, token, url, want string }{
		{"Chosen search", "", "https://example.test/chosen", "invalid selection_token"},
		{"Changed", token, "https://example.test/chosen", "original request"},
		{"Chosen search", token, "https://example.test/other", "candidate_url"},
		{"Chosen search", token, "https://example.test/chosen", "selected acquisition reached"},
	} {
		raw, _ := json.Marshal(map[string]any{"identifier": "1", "query": tc.query, "selection_token": tc.token, "candidate_url": tc.url})
		_, _, err := s.RunOperation(context.Background(), "track.upgrade", raw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("continuation %s: %v", raw, err)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid selections invoked acquisition: %d", calls)
	}
}

func TestMediaReadsResolveCollectionAndStagingTargets(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	store := staging.New(lib)
	item, err := store.Create(ctx, staging.Item{Kind: "track", Title: "Staged", Status: "ready"})
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
		t.Fatal("media read guarded")
		return nil, nil
	}
	var calledPath string
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		calledPath = args[len(args)-1]
		if tool == "fetch-track" {
			return []byte(`{"status":"success"}`), nil
		}
		return []byte(`{"status":"success","results":[{"status":"success","inspection":{"title":"Actual tags"}}]}`), nil
	}
	for _, op := range s.mediaOperations() {
		s.operations[op.Name] = op
	}
	for _, target := range []struct{ raw, path string }{{fmt.Sprintf(`{"staging_id":%q}`, item.ID), path}, {`{"identifier":"1"}`, filepath.Join(lib, "song.mp3")}} {
		for _, operation := range []string{"track.verify", "track.tags.inspect"} {
			if _, id, err := s.RunOperation(ctx, operation, json.RawMessage(target.raw)); err != nil || id != "" || calledPath != target.path {
				t.Fatalf("media read %s: %s %s %v", operation, calledPath, id, err)
			}
		}
	}
	for _, raw := range []string{`{"identifier":"999"}`, `{"staging_id":"missing"}`} {
		if _, _, err := s.RunOperation(ctx, "track.verify", json.RawMessage(raw)); err == nil {
			t.Fatalf("resolved missing media %s", raw)
		}
	}
	if _, _, err := s.RunOperation(ctx, "staging.retry", json.RawMessage(`{"id":"missing"}`)); err == nil {
		t.Fatal("retried missing acquisition")
	}
}

func TestEmbeddedMediaEditsUseTargetMutationScope(t *testing.T) {
	s, _ := testAPI(t)
	guardCalls := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		guardCalls++
		return nil, errors.New("collection guard")
	}
	for _, operation := range []string{"track.tags.update", "track.artwork.embed"} {
		for _, staged := range []bool{false, true} {
			target := `"identifier":"999"`
			if staged {
				target = `"staging_id":"missing"`
			}
			_, _, err := s.RunOperation(context.Background(), operation, json.RawMessage(`{`+target+`,"artwork":"cover.png"}`))
			if err == nil || strings.Contains(err.Error(), "collection guard") == staged {
				t.Fatalf("scope %s staged=%v: %v", operation, staged, err)
			}
		}
	}
	if guardCalls != 2 {
		t.Fatalf("guard calls %d", guardCalls)
	}
}

func TestAcquisitionFetchStagesAndOptionallyImports(t *testing.T) {
	for _, kind := range []string{"track", "mix"} {
		for _, imported := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/import=%t", kind, imported), func(t *testing.T) {
				s, lib := testAPI(t)
				guards := 0
				s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
					guards++
					return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
				}
				s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
					if tool == "ffprobe" {
						return []byte(`{"format":{"tags":{"title":"Imported title"}},"streams":[]}`), nil
					}
					if slices.Contains(args, "--dry-run") {
						return []byte(`{"status":"preview","title":"Mix"}`), nil
					}
					index := slices.Index(args, "--out-dir")
					if index < 0 {
						t.Fatalf("no output directory %s %v", tool, args)
					}
					path := filepath.Join(args[index+1], "download.mp3")
					if err := os.WriteFile(path, []byte("downloaded"), 0600); err != nil {
						t.Fatal(err)
					}
					if kind == "mix" {
						return json.Marshal(map[string]any{"status": "success", "title": "Mix", "tracks": []map[string]any{{"index": 1, "title": "Song", "status": "completed", "output_file": path}}})
					}
					return json.Marshal(map[string]any{"status": "success", "output_file": path})
				}
				for _, op := range s.acquisitionOperations() {
					s.operations[op.Name] = op
				}
				if kind == "mix" {
					if _, _, err := s.RunOperation(context.Background(), "mix.inspect", json.RawMessage(`{"query":"Mix"}`)); err != nil {
						t.Fatal(err)
					}
				}
				raw, _ := json.Marshal(map[string]any{"query": "Song", "import": imported})
				result, id, err := s.RunOperation(context.Background(), kind+".fetch", raw)
				if err != nil || id == "" {
					t.Fatalf("fetch %s %v", id, err)
				}
				if report, ok := result.(*acquisition.TrackReport); ok && report.Item.Status != "ready" {
					t.Fatalf("track fetch failure: %+v", report.Item)
				}
				items, err := staging.New(lib).List(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if imported {
					report := result.(map[string]any)["import"].(*staging.ImportReport)
					if len(report.Imported) != 1 || len(report.Failed) != 0 || len(items) != 0 || guards != 1 {
						t.Fatalf("import %+v staged=%v guards=%d", report, items, guards)
					}
				} else {
					if len(items) == 0 || guards != 0 {
						t.Fatalf("staging %+v guards=%d", items, guards)
					}
					if kind == "track" && result.(*acquisition.TrackReport).Item.Status != "ready" {
						t.Fatal("download not ready")
					}
				}
			})
		}
	}
}

func TestStagedTagEditCommitsOnceAndUndoes(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	store := staging.New(lib)
	item, err := store.Create(ctx, staging.Item{Kind: "track", Title: "Before", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/audio.mp3"
	if err := os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("staged tag used collection guard")
		return nil, nil
	}
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffmpeg" {
			return []byte("SHA256=same-packets"), nil
		}
		if tool == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		if slices.Contains(args, "inspect") {
			return []byte(`{"status":"success","results":[{"status":"success","inspection":{"title":"After"}}]}`), nil
		}
		index := slices.Index(args, "--out-dir")
		if index < 0 {
			t.Fatalf("tag output dir %v", args)
		}
		output := filepath.Join(args[index+1], "tagged.mp3")
		if err := os.WriteFile(output, []byte("tagged"), 0600); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "output_file": output}}})
	}
	for _, op := range s.mediaOperations() {
		s.operations[op.Name] = op
	}
	_, id, err := s.RunOperation(ctx, "track.tags.update", json.RawMessage(fmt.Sprintf(`{"staging_id":%q,"title":"After"}`, item.ID)))
	if err != nil || id == "" {
		t.Fatalf("tag edit %s %v", id, err)
	}
	updated, err := store.Inspect(ctx, item.ID)
	if err != nil || updated.Title != "After" || updated.AudioPath == item.AudioPath {
		t.Fatalf("updated %+v %v", updated, err)
	}
	if _, err := history.Undo(ctx, lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Inspect(ctx, item.ID)
	if err != nil || restored.Title != "Before" || restored.AudioPath != item.AudioPath {
		t.Fatalf("restored %+v %v", restored, err)
	}
}

func TestLookupEffectsAndStrictParameters(t *testing.T) {
	s, lib := testAPI(t)
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("lookup used collection guard")
		return nil, nil
	}
	calls := 0
	s.config.ToolRunner = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte(`{"status":"success","tracks":[]}`), nil
	}
	for _, op := range s.acquisitionOperations() {
		s.operations[op.Name] = op
	}
	before := acquisitionTree(t, lib)
	_, id, err := s.RunOperation(context.Background(), "mix.inspect", json.RawMessage(`{"query":"Mix"}`))
	if err != nil || id != "" {
		t.Fatalf("inspect wrote history %s: %v", id, err)
	}
	if after := acquisitionTree(t, lib); !reflect.DeepEqual(before, after) {
		t.Fatal("inspect wrote library files")
	}
	for _, name := range []string{"track.candidates", "mix.candidates", "mix.inspect"} {
		for _, key := range []string{"import", "to_playlist", "selection_token", "candidate_url"} {
			raw, _ := json.Marshal(map[string]any{"query": "Mix", key: map[string]any{"import": true, "to_playlist": "1", "selection_token": "ignored", "candidate_url": "https://example.test/ignored"}[key]})
			if _, _, err := s.RunOperation(context.Background(), name, raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("%s accepted ignored %s: %v", name, key, err)
			}
		}
	}
	if _, _, err := s.RunOperation(context.Background(), "track.tags.inspect", json.RawMessage(`{"identifier":"1","min_bandwidth_khz":17}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("tag inspect accepted ignored bandwidth: %v", err)
	}
	if calls != 1 {
		t.Fatalf("invalid lookups invoked tool: %d", calls)
	}
}

func TestVerifyAutoInstallAndTagCommentPreview(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("original audio"), 0600); err != nil {
		t.Fatal(err)
	}
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "fetch-track" {
			if !slices.Contains(args, "--auto-install") {
				t.Fatalf("verify omitted native install flag: %v", args)
			}
			return []byte(`{"status":"success"}`), nil
		}
		return []byte(`{"status":"success","results":[{"status":"success","dry_run":true,"metadata":{"comment":"","title":"Song"}}]}`), nil
	}
	for _, op := range s.mediaOperations() {
		s.operations[op.Name] = op
	}
	if _, _, err := s.RunOperation(context.Background(), "track.verify", json.RawMessage(`{"identifier":"1","auto_install":true}`)); err != nil {
		t.Fatal(err)
	}
	result, _, err := s.RunOperation(context.Background(), "track.tags.update", json.RawMessage(`{"identifier":"1","comment":"","dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"comment":""`) {
		t.Fatalf("preview omitted cleared comment: %s", raw)
	}
}

func TestFailedAcquisitionJobsRetainStagingReports(t *testing.T) {
	for _, kind := range []string{"track", "mix"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := testAPI(t)
			s.config.ToolRunner = func(context.Context, string, ...string) ([]byte, error) {
				return []byte(`{"status":"error","error":"download unavailable","tracks":[]}`), nil
			}
			for _, op := range s.acquisitionOperations() {
				s.operations[op.Name] = op
			}
			id := submitAPIJob(t, s, kind+".fetch", `{"query":"Unavailable"}`)
			job := awaitAPIJob(t, s, id)
			if job["state"] != "failed" || job["history_id"] == nil || job["error"] == nil {
				t.Fatalf("failure outcome: %+v", job)
			}
			result, ok := job["result"].(map[string]any)
			if !ok {
				t.Fatalf("missing structured report: %+v", job)
			}
			item, ok := result["item"].(map[string]any)
			if !ok || item["status"] != "failed" || item["id"] == "" {
				t.Fatalf("missing failed staged item: %+v", result)
			}
		})
	}
}
