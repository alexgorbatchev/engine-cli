package acquisition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestCandidateSelectionBindsRequestAndURL(t *testing.T) {
	s := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"candidates","candidates":[{"webpage_url":"https://example.org/song"}]}`), nil
	}}}
	request := TrackRequest{Query: "artist song"}
	result, err := s.TrackCandidates(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateSelection(context.Background(), result.SelectionToken, "track", request, "https://example.org/song"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		request TrackRequest
		url     string
	}{{TrackRequest{Query: "another"}, "https://example.org/song"}, {request, "https://example.org/other"}} {
		if err = s.ValidateSelection(context.Background(), result.SelectionToken, "track", bad.request, bad.url); err == nil {
			t.Fatal("accepted altered selection")
		}
	}
	if err = s.ValidateSelection(context.Background(), "../escape", "track", request, "https://example.org/song"); err == nil {
		t.Fatal("accepted invalid token")
	}
}

func TestSelectionFailuresUseStableCodes(t *testing.T) {
	ctx := context.Background()
	s := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"candidates","candidates":[{"webpage_url":"https://example.org/song"}]}`), nil
	}}}
	request := TrackRequest{Query: "artist song"}
	if code := progress.ErrorCode(s.ValidateSelection(ctx, strings.Repeat("0", 64), "track", request, "https://example.org/song")); code != "selection_expired" {
		t.Fatalf("missing directory: %q", code)
	}
	result, err := s.TrackCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token, kind string
		request     TrackRequest
		url, code   string
	}{
		{"../escape", "track", request, "https://example.org/song", "invalid_selection_token"},
		{strings.Repeat("0", 64), "track", request, "https://example.org/song", "selection_expired"},
		{result.SelectionToken, "mix", request, "https://example.org/song", "selection_mismatch"},
		{result.SelectionToken, "track", TrackRequest{Query: "another"}, "https://example.org/song", "selection_mismatch"},
		{result.SelectionToken, "track", request, "https://example.org/other", "candidate_not_listed"},
		{result.SelectionToken, "track", request, "", "candidate_not_listed"},
	} {
		if code := progress.ErrorCode(s.ValidateSelection(ctx, tc.token, tc.kind, tc.request, tc.url)); code != tc.code {
			t.Fatalf("%+v: code %q", tc, code)
		}
	}
	if err := os.WriteFile(filepath.Join(s.selectionDirectory(), result.SelectionToken+".json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := progress.ErrorCode(s.ValidateSelection(ctx, result.SelectionToken, "track", request, "https://example.org/song")); code != "selection_expired" {
		t.Fatalf("unreadable selection: %q", code)
	}
}

func TestSearchesKeepBoundedSelections(t *testing.T) {
	ctx := context.Background()
	s := Service{Library: t.TempDir()}
	now := time.Now()
	if err := os.MkdirAll(s.selectionDirectory(), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, age time.Duration) {
		path := filepath.Join(s.selectionDirectory(), name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	write("expired.json", selectionRetention+time.Minute)
	write("fresh.json", time.Minute)
	if _, err := s.saveSelection(ctx, "track", TrackRequest{Query: "first"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.selectionDirectory(), "expired.json")); !os.IsNotExist(err) {
		t.Fatalf("kept an expired selection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.selectionDirectory(), "fresh.json")); err != nil {
		t.Fatalf("pruned a fresh selection: %v", err)
	}
	entries, err := os.ReadDir(s.selectionDirectory())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(s.selectionDirectory(), entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	write("unrelated.txt", 48*time.Hour)
	for index := range maxRetainedSelection + 5 {
		write(fmt.Sprintf("recent-%03d.json", index), time.Duration(index)*time.Second)
	}
	if err := os.Mkdir(filepath.Join(s.selectionDirectory(), "nested.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	token, err := s.saveSelection(ctx, "track", TrackRequest{Query: "song"}, []string{"https://example.org/song"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(s.selectionDirectory())
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, entry := range entries {
		kept[entry.Name()] = true
	}
	if !kept[token+".json"] || !kept["unrelated.txt"] || !kept["nested.json"] {
		t.Fatalf("kept %v", kept)
	}
	if !kept["recent-000.json"] || !kept[fmt.Sprintf("recent-%03d.json", maxRetainedSelection-2)] || kept[fmt.Sprintf("recent-%03d.json", maxRetainedSelection-1)] {
		t.Fatalf("retained the wrong selections: %d entries", len(entries))
	}
	// A library without a selection directory has nothing to prune.
	Service{Library: t.TempDir()}.pruneSelections(now, "")
}
