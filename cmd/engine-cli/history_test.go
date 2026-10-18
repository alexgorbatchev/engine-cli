package main

import (
	"strings"
	"testing"
)

func TestHistoryCommands(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("AGENT", "1")
	lib := createTestLibrary(t)
	out, err := executeCommand("-l", lib, "--no-backup", "playlist", "create", "Undo me")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "operation_id:") {
		t.Fatalf("missing operation ID: %s", out)
	}
	out, err = executeCommand("-l", lib, "library", "history")
	if err != nil || !strings.Contains(out, "playlist.create") {
		t.Fatalf("history=%s err=%v", out, err)
	}
	if out, err = executeCommand("-l", lib, "library", "undo", "--dry-run"); err != nil {
		t.Fatalf("preview=%s err=%v", out, err)
	}
	if out, err = executeCommand("-l", lib, "--no-backup", "library", "undo"); err != nil {
		t.Fatalf("undo=%s err=%v", out, err)
	}
	out, err = executeCommand("-l", lib, "playlist", "list")
	if err != nil || strings.Contains(out, "Undo me") {
		t.Fatalf("playlists=%s err=%v", out, err)
	}
}
