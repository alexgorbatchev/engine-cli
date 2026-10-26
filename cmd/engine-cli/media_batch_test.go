package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMediaCLINativeTargetsAndBatches(t *testing.T) {
	original := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = original })
	for _, tc := range []struct {
		name    string
		args    []string
		targets []string
	}{
		{"remote verify", []string{"track", "verify", "https://example.test/music.mp3", "--json"}, []string{"https://example.test/music.mp3"}},
		{"explicit local verify", []string{"track", "verify", "--target", "relative.mp3", "--json"}, []string{"relative.mp3"}},
		{"tag paths", []string{"track", "tags", "inspect", "first.mp3", "second.mp3", "--json"}, []string{"first.mp3", "second.mp3"}},
		{"single tag path", []string{"track", "tags", "inspect", "single.mp3", "--json"}, []string{"single.mp3"}},
		{"explicit tag path", []string{"track", "tags", "inspect", "--path", "1", "--json"}, []string{"1"}},
		{"explicit tag paths", []string{"track", "tags", "inspect", "--paths", "one.mp3,two.mp3", "--json"}, []string{"one.mp3", "two.mp3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var targets []string
			acquisitionRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
				target := args[len(args)-1]
				targets = append(targets, target)
				if tool == "fetch-track" {
					return []byte(`{"status":"success"}`), nil
				}
				return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "input_file": target}}})
			}
			out, err := executeCommand(append([]string{"-l", t.TempDir()}, tc.args...)...)
			if err != nil || !slices.Equal(targets, tc.targets) || !strings.Contains(out, "success") {
				t.Fatalf("targets=%v output=%s error=%v", targets, out, err)
			}
		})
	}
	lib := createTestLibrary(t)
	var targets []string
	acquisitionRunner = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		targets = append(targets, filepath.Base(args[len(args)-1]))
		return []byte(`{"status":"success","results":[{"status":"success"}]}`), nil
	}
	// A partial batch prints every result yet exits unsuccessfully.
	if out, err := executeCommand("-l", lib, "track", "tags", "inspect", "1", "999", "1", "--json"); err == nil || len(targets) != 2 || !strings.Contains(out, `"status":"partial"`) {
		t.Fatalf("collection batch=%v error=%v output=%s", targets, err, out)
	}
	if _, err := executeCommand("-l", lib, "track", "tags", "inspect", "1", "--json"); err != nil || len(targets) != 3 {
		t.Fatalf("single collection target=%v error=%v", targets, err)
	}
	if _, err := executeCommand("-l", lib, "track", "tags", "inspect", "--staging-ids", "missing,also-missing", "--json"); err == nil {
		t.Fatalf("staged batch error report: %v", err)
	}
}
