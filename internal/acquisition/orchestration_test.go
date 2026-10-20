package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/acquire"
	"github.com/dj/engine-cli/internal/staging"
)

func argValue(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func contains(args []string, key string) bool {
	for _, arg := range args {
		if arg == key {
			return true
		}
	}
	return false
}
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMixDoesNotStageIncompleteNativeOutput(t *testing.T) {
	service := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		path := filepath.Join(argValue(args, "--out-dir"), "partial.flac")
		writeFixture(t, path, "partial bytes")
		return json.Marshal(acquire.MixResult{Status: "partial", Tracks: []acquire.MixTrack{{Index: 1, Title: "Incomplete", Status: "failed", OutputFile: path}}})
	}}}
	report, err := service.FetchMix(context.Background(), MixRequest{Query: "mix"})
	if err == nil || report == nil || len(report.Item.Items) != 1 || report.Item.Items[0].Status != "failed" || report.Item.Items[0].AudioPath != "" {
		t.Fatalf("incomplete output accepted: %+v %v", report, err)
	}
}

func TestMixRetryOnlyFailedChildren(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	var queries []string
	service := Service{Library: lib, Client: acquire.Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		work := argValue(args, "--out-dir")
		if tool == "fetch-mix" {
			path := filepath.Join(work, "first.flac")
			writeFixture(t, path, "first")
			writeFixture(t, filepath.Join(work, "upstream-manifest.json"), "{}")
			return json.Marshal(acquire.MixResult{Status: "partial", Title: "Mix", SourceURL: "https://example.org/mix", Tracks: []acquire.MixTrack{{Index: 1, Artist: "Artist", Title: "First", Status: "completed", OutputFile: path}, {Index: 2, Artist: "Artist", Title: "Second", Status: "error", Error: "offline"}}})
		}
		queries = append(queries, args[len(args)-1])
		path := filepath.Join(work, "retry.flac")
		writeFixture(t, path, "second")
		return json.Marshal(acquire.TrackResult{Status: "success", OutputFile: path, Metadata: &acquire.Metadata{Title: "Second", Artist: "Artist"}})
	}}}
	report, err := service.FetchMix(ctx, MixRequest{Query: "Artist mix", Options: acquire.MixOptions{Sources: "bandcamp"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Item.Status != "failed" || len(report.Item.Items) != 2 || report.Item.Items[0].Status != "ready" {
		t.Fatalf("report=%+v", report.Item)
	}
	first := report.Item.Items[0]
	failed := report.Item.Items[1]
	result, err := service.Retry(ctx, report.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	retried := result.(*staging.Item)
	if retried.ID != report.Item.ID || retried.Status != "ready" || retried.Items[0].ID != first.ID || retried.Items[1].ID != failed.ID || retried.Items[1].Index != 2 {
		t.Fatalf("retried=%+v", retried)
	}
	if len(queries) != 1 || queries[0] != "Artist - Second" {
		t.Fatalf("queries=%v", queries)
	}
	original, err := staging.New(lib).Resolve(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "first" {
		t.Fatalf("original changed: %q %v", data, err)
	}
}

func TestRetryEmptyMixKeepsIdentity(t *testing.T) {
	ctx := context.Background()
	service := Service{Library: t.TempDir()}
	calls := 0
	service.Client.Runner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		calls++
		if calls == 1 {
			return []byte(`{"status":"error","error":"offline"}`), nil
		}
		path := filepath.Join(argValue(args, "--out-dir"), "recovered.flac")
		writeFixture(t, path, "recovered")
		return json.Marshal(acquire.MixResult{Status: "success", Title: "Recovered", Tracks: []acquire.MixTrack{{Index: 1, Title: "Recovered", OutputFile: path, Status: "completed"}}})
	}
	report, err := service.FetchMix(ctx, MixRequest{Query: "mix"})
	if err == nil {
		t.Fatal("failed mix reported success")
	}
	retried, err := service.Retry(ctx, report.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.(*MixReport).Item.ID != report.Item.ID || retried.(*MixReport).Item.Status != "ready" {
		t.Fatalf("retry=%+v", retried)
	}
	items, err := staging.New(service.Library).List(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%v error=%v", items, err)
	}
}

func TestCancelledAcquisitionPersistsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) { cancel(); return nil, context.Canceled }}}
	report, err := service.FetchTrack(ctx, TrackRequest{Query: "song"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	item, err := staging.New(service.Library).Inspect(context.Background(), report.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "failed" || item.Error == "" {
		t.Fatalf("item=%+v", item)
	}
}

func TestStagedTagPreservesAudioAndRefreshesArtwork(t *testing.T) {
	for _, action := range []string{"update", "artwork"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			lib := t.TempDir()
			store := staging.New(lib)
			item, err := store.Create(ctx, staging.Item{Kind: "track", Title: "Before"})
			if err != nil {
				t.Fatal(err)
			}
			dir, err := store.Directory(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(dir, "before.flac")
			writeFixture(t, original, "original packets")
			item.AudioPath, err = store.Relative(original)
			if err != nil {
				t.Fatal(err)
			}
			item.Status = "ready"
			if err = store.Update(ctx, *item); err != nil {
				t.Fatal(err)
			}
			service := Service{Library: lib, Client: acquire.Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
				switch tool {
				case "tag-track":
					if contains(args, "--dry-run") {
						return []byte(`{"status":"success","results":[{"status":"success","dry_run":true}]}`), nil
					}
					if contains(args, "inspect") {
						return []byte(`{"status":"success","results":[{"status":"success","inspection":{"title":"After","artist":"Artist","album":"Album","has_artwork":true}}]}`), nil
					}
					path := filepath.Join(argValue(args, "--out-dir"), "tagged.flac")
					writeFixture(t, path, "tagged packets")
					return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: path}}})
				case "ffprobe":
					return []byte(`{"streams":[{"index":1,"disposition":{"attached_pic":1}}]}`), nil
				case "ffmpeg":
					if contains(args, "hash") {
						return []byte("SHA256=identical\n"), nil
					}
					writeFixture(t, args[len(args)-1], "preview")
					return nil, nil
				}
				return nil, errors.New("unexpected tool")
			}}}
			inspected, err := service.TagStaged(ctx, item.ID, "inspect", acquire.TagOptions{})
			if err != nil || inspected.Result.Results[0].Inspection.Title != "After" {
				t.Fatalf("inspect=%v %v", inspected, err)
			}
			dry, err := service.TagStaged(ctx, item.ID, "update", acquire.TagOptions{DryRun: true})
			if err != nil || dry.Item.AudioPath != item.AudioPath {
				t.Fatalf("dry=%v %v", dry, err)
			}
			result, err := service.TagStaged(ctx, item.ID, action, acquire.TagOptions{Title: new("After"), Artwork: "cover.jpg"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Item.Title != "After" || result.Item.AudioPath == item.AudioPath || result.Item.ArtworkPath == "" {
				t.Fatalf("item=%+v", result.Item)
			}
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "original packets" {
				t.Fatalf("original=%q %v", data, err)
			}
		})
	}
}

func TestAcquisitionRejectsUnownedOutput(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.flac")
	writeFixture(t, outside, "must remain")
	service := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return json.Marshal(acquire.TrackResult{Status: "success", OutputFile: outside})
	}}}
	result, err := service.FetchTrack(context.Background(), TrackRequest{Query: "song"})
	if err == nil {
		t.Fatal("unowned output reported success")
	}
	if result.Item.Status != "failed" || !strings.Contains(result.Item.Error, "outside") {
		t.Fatalf("item=%+v", result.Item)
	}
}
