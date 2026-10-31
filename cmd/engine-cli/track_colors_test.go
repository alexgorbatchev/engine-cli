package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestTrackColorCommandsRequireExplicitClearAndSupportUndo(t *testing.T) {
	library := createTestLibrary(t)
	output := executeTrackColorJSON(t, "--lib-dir", library, "track", "color", "list", "--json")
	var initial colors.Collection
	if err := json.Unmarshal([]byte(output), &initial); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"colors.db", "tags.db"} {
		if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
			t.Fatalf("read enrolled %s: %v", name, err)
		}
	}
	output, err := executeCommand("--lib-dir", library, "--no-backup", "track", "color", "update", "1", "--color", "#aB1234", "--revision", initial.Revision)
	if err != nil || !strings.Contains(output, "Color: #AB1234\n") || !strings.Contains(output, "Changed: true\n") {
		t.Fatalf("human assignment=%s error=%v", output, err)
	}
	output = executeTrackColorJSON(t, "--lib-dir", library, "track", "color", "inspect", "1", "--json")
	var assigned colors.TrackColor
	if err := json.Unmarshal([]byte(output), &assigned); err != nil {
		t.Fatal(err)
	}
	args := []string{"--lib-dir", library, "--no-backup", "track", "color", "update", "1", "--revision", assigned.Revision}
	var stdout, stderr bytes.Buffer
	command := newRootCommand()
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(args)
	if err := command.Execute(); err == nil || stdout.Len() != 0 {
		t.Fatalf("missing color cleared stored value: output=%s error=%v", stdout.String(), err)
	}
	output, err = executeCommand(append(args, "--color=")...)
	if err != nil || !strings.Contains(output, "Color: unassigned\n") {
		t.Fatalf("explicit clear=%s error=%v", output, err)
	}
	entries, err := history.List(library)
	if err != nil || len(entries) != 2 {
		t.Fatalf("assignment/clear history=%+v %v", entries, err)
	}
	if _, err := history.Undo(t.Context(), library, entries[0].ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	output, err = executeCommand("--lib-dir", library, "track", "color", "list")
	if err != nil || !strings.Contains(output, "Track 1: #AB1234\n") {
		t.Fatalf("undo color=%s error=%v", output, err)
	}
}

func executeTrackColorJSON(t *testing.T, args ...string) string {
	t.Helper()
	var output, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatalf("track color command: %v\n%s", err, diagnostics.String())
	}
	return output.String()
}
