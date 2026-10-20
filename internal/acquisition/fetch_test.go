package acquisition

import (
	"context"
	"encoding/json"
	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchStagesWithoutImport(t *testing.T) {
	lib := t.TempDir()
	service := Service{Library: lib, Client: acquire.Client{Runner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		var work string
		for i, a := range args {
			if a == "--out-dir" {
				work = args[i+1]
			}
		}
		path := filepath.Join(work, "Artist - Song.flac")
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(acquire.TrackResult{Status: "success", OutputFile: path, Metadata: &acquire.Metadata{Title: "Song", Artist: "Artist"}})
	}}}
	r, err := service.FetchTrack(context.Background(), TrackRequest{Query: "Artist Song"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Item.Status != "ready" || r.Item.Artist != "Artist" || r.Item.Title != "Song" {
		t.Fatalf("item=%+v", r.Item)
	}
	got, err := staging.New(lib).Resolve(context.Background(), r.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil || string(data) != "fixture" {
		t.Fatalf("data=%q error=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Database2")); !os.IsNotExist(err) {
		t.Fatalf("acquisition touched collection: %v", err)
	}
}

func TestFailedAcquisitionRetainsRetryRecord(t *testing.T) {
	lib := t.TempDir()
	service := Service{Library: lib, Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"error","error":"no candidates"}`), nil
	}}}
	r, err := service.FetchTrack(context.Background(), TrackRequest{Query: "missing"})
	if err == nil {
		t.Fatal("failed download reported success")
	}
	if r.Item.Status != "failed" || r.Item.Error == "" {
		t.Fatalf("item=%+v", r.Item)
	}
	items, err := staging.New(lib).List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v error=%v", items, err)
	}
}
