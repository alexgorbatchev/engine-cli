package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTagEditPassesJSONEditsToTheOperation(t *testing.T) {
	lib := t.TempDir()
	createEmptyTestDatabase(t, filepath.Join(lib, "Database2", "m.db"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := executeCommand("track", "tags", "edit", "--lib-dir", lib, "--edits", "not json", "--dry-run"); err == nil || !strings.Contains(err.Error(), "--edits must be JSON") {
		t.Fatalf("invalid JSON: %v", err)
	}
	// The array reaches the operation's own validation, so it arrived structured.
	if _, err := executeCommand("track", "tags", "edit", "--lib-dir", lib, "--edits", `[{"identifier":" "}]`, "--dry-run"); err == nil || !strings.Contains(err.Error(), "every edit needs an identifier") {
		t.Fatalf("structured edits: %v", err)
	}
}
