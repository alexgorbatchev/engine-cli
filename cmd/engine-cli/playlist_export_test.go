package main

import (
	"bytes"
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaylistExportCLIExactReviewAndNoOverwrite(t *testing.T) {
	root := createTestLibrary(t)
	path := filepath.Join(root, "set.m3u8")
	args := []string{"-l", root, "playlist", "export", "1", "--path", path, "--path-mode", "relative"}
	preview, err := executeCommand(append(append([]string{}, args...), "--dry-run")...)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Playlist:", "Destination:", "Media paths: relative", "Plan token:", "existing destinations are never replaced"} {
		if !strings.Contains(preview, text) {
			t.Fatalf("missing %q: %s", text, preview)
		}
	}
	if strings.Contains(preview, `"plan_token"`) {
		t.Fatal("human output was raw JSON")
	}
	plan := runJSON(t, root, "playlist", "export", "1", "--path", path, "--path-mode", "relative", "--dry-run", "--json")
	if _, err := executeCommand(args...); err == nil {
		t.Fatal("unreviewed export accepted")
	}
	output, err := executeCommand(append(args, "--plan-token", plan["plan_token"].(string))...)
	if err != nil || !strings.Contains(output, "Source collection unchanged") {
		t.Fatalf("apply: %s %v", output, err)
	}
	before, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(before), "#EXTM3U\n") {
		t.Fatalf("output: %q %v", before, err)
	}
	if _, err := executeCommand(append(args, "--plan-token", plan["plan_token"].(string))...); err == nil {
		t.Fatal("existing file replaced")
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("conflict altered file: %q %v", after, err)
	}
}

func TestPlaylistExportFailureWritesNoResult(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, asJSON := range []bool{false, true} {
			name := fmt.Sprintf("preview=%t/json=%t", dryRun, asJSON)
			t.Run(name, func(t *testing.T) {
				root := createTestLibrary(t)
				args := []string{"-l", root, "playlist", "export", "99999", "--path", filepath.Join(root, "new.m3u8")}
				if dryRun {
					args = append(args, "--dry-run")
				}
				if asJSON {
					args = append(args, "--json")
				}
				var out bytes.Buffer
				command := newRootCommand()
				command.SetOut(&out)
				command.SetArgs(args)
				err := command.Execute()
				if progress.ErrorCode(err) != "playlist_export_target" || out.Len() != 0 {
					t.Fatalf("failed export emitted result: %q %v", out.String(), err)
				}
			})
		}
	}
}
