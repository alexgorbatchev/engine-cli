package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTagUpdateLooksUpOnlyWhenRequested(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("original audio"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(`{"status":"success","results":[{"status":"success","dry_run":true,"metadata":{"title":"Song","source":"Existing tags"}}]}`), nil
	}
	for _, tc := range []struct {
		request    string
		wantLookup bool
	}{
		{`{"identifier":"1","title":"Song","dry_run":true}`, false},
		{`{"identifier":"1","title":"Song","lookup":"none","dry_run":true}`, false},
		{`{"identifier":"1","title":"Song","lookup":"replace","refresh":true,"source_url":"https://example.test/a","dry_run":true}`, true},
	} {
		calls = nil
		if _, _, err := s.RunOperation(context.Background(), "track.tags.update", json.RawMessage(tc.request)); err != nil {
			t.Fatalf("%s: %v", tc.request, err)
		}
		if len(calls) != 1 || slices.Contains(calls[0], "--no-lookup") == tc.wantLookup {
			t.Fatalf("%s ran %q", tc.request, calls)
		}
	}
	if got := s.operations["track.tags.update"].Parameters["lookup"]; got != "string" {
		t.Fatalf("catalog lookup type %q", got)
	}
}

func TestTagUpdateRejectsLookupOptionsWithoutALookup(t *testing.T) {
	s, _ := testAPI(t)
	op := s.operations["track.tags.update"]
	for raw, want := range map[string]string{
		`{"identifier":"1","lookup":"fill_missing"}`:                  "lookup must be none or replace",
		`{"identifier":"1","source_url":"https://example.test/a"}`:    "source_url is recorded only with lookup replace",
		`{"identifier":"1","lookup":"none","source_url":"https://a"}`: "source_url is recorded only with lookup replace",
	} {
		if _, err := op.bind(json.RawMessage(raw)); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"identifier":"1","lookup":"none","refresh":true,"no_cache":true}`, `{"identifier":"1","lookup":"replace"}`} {
		if _, err := op.bind(json.RawMessage(raw)); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
