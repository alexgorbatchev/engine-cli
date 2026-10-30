package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestPlaylistImportMissingSourceReturnsError(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "human"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			root := createTestLibrary(t)
			args := []string{"-l", root, "playlist", "import", filepath.Join(root, "absent.m3u8"), "--name", "Set", "--dry-run"}
			if asJSON {
				args = append(args, "--json")
			}
			var out, diagnostics bytes.Buffer
			command := newRootCommand()
			command.SetOut(&out)
			command.SetErr(&diagnostics)
			command.SetArgs(args)
			err := command.Execute()
			if progress.ErrorCode(err) != "playlist_import_unavailable" || strings.TrimSpace(out.String()) != "" {
				t.Fatalf("missing source produced a result: output=%q err=%v", out.String(), err)
			}
			entries, err := history.List(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed preview created undo history: %v %v", entries, err)
			}
		})
	}
}
