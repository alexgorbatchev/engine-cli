package acquisition

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestAcquisitionHistoryRecordsOnlyUndoableChanges(t *testing.T) {
	tests := []struct {
		name     string
		result   func(work string) any
		run      func(ctx context.Context, s Service) error
		recorded bool
		items    int
	}{
		{
			name:   "failed track fetch with partial download",
			result: partialFailure,
			run: func(ctx context.Context, s Service) error {
				_, err := s.FetchTrack(ctx, TrackRequest{Query: "missing"})
				return err
			},
			items: 1,
		},
		{
			name: "failed mix fetch",
			result: func(string) any {
				return acquire.MixResult{Status: "error", Error: "no tracks", Tracks: []acquire.MixTrack{{Index: 1, Title: "Song", Artist: "Artist", Status: "failed", Error: "no candidates"}}}
			},
			run: func(ctx context.Context, s Service) error {
				_, err := s.FetchMix(ctx, MixRequest{Query: "set"})
				return err
			},
			items: 2,
		},
		{
			name:   "successful track fetch",
			result: download,
			run: func(ctx context.Context, s Service) error {
				_, err := s.FetchTrack(ctx, TrackRequest{Query: "Artist Song"})
				return err
			},
			recorded: true,
			items:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lib := t.TempDir()
			s := Service{Library: lib, Client: acquire.Client{Runner: toolRunner(t, tt.result)}}
			ctx := context.Background()
			entry, err := history.Run(ctx, lib, "track.fetch", nil, func(ctx context.Context) error { return tt.run(ctx, s) })
			if (err == nil) != tt.recorded {
				t.Fatalf("err=%v", err)
			}
			assertHistory(t, lib, entry, tt.recorded)
			items, err := staging.New(lib).List(ctx)
			if err != nil || len(items) != tt.items {
				t.Fatalf("items=%+v err=%v", items, err)
			}
		})
	}
}

func TestFailedRetryLeavesNoHistory(t *testing.T) {
	lib := t.TempDir()
	s := Service{Library: lib, Client: acquire.Client{Runner: toolRunner(t, partialFailure)}}
	ctx := context.Background()
	report, err := s.FetchTrack(ctx, TrackRequest{Query: "missing"})
	if err == nil {
		t.Fatal("failed download reported success")
	}
	entry, err := history.Run(ctx, lib, "staging.retry", nil, func(ctx context.Context) error {
		_, err := s.Retry(ctx, report.Item.ID)
		return err
	})
	if err == nil {
		t.Fatal("failed retry reported success")
	}
	assertHistory(t, lib, entry, false)
	item, err := staging.New(lib).Inspect(ctx, report.Item.ID)
	if err != nil || item.Status != "failed" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}

func toolRunner(t *testing.T, result func(work string) any) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		var work string
		for i, arg := range args {
			if arg == "--out-dir" {
				work = args[i+1]
			}
		}
		return json.Marshal(result(work))
	}
}

func partialFailure(work string) any {
	if err := os.WriteFile(filepath.Join(work, "partial.part"), []byte("partial"), 0o600); err != nil {
		return acquire.TrackResult{Status: "error", Error: err.Error()}
	}
	return acquire.TrackResult{Status: "error", Error: "no candidates"}
}

func download(work string) any {
	path := filepath.Join(work, "Artist - Song.flac")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		return acquire.TrackResult{Status: "error", Error: err.Error()}
	}
	return acquire.TrackResult{Status: "success", OutputFile: path, Metadata: &acquire.Metadata{Title: "Song", Artist: "Artist"}}
}

func assertHistory(t *testing.T, lib string, entry *history.Entry, recorded bool) {
	t.Helper()
	list, err := history.List(lib)
	if err != nil {
		t.Fatal(err)
	}
	if recorded {
		if entry == nil || len(list) != 1 {
			t.Fatalf("entry=%+v history=%+v", entry, list)
		}
		return
	}
	if entry != nil || len(list) != 0 {
		t.Fatalf("failed acquisition recorded undo history: entry=%+v history=%+v", entry, list)
	}
}
