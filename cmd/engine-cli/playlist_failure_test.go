package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestPlaylistListRejectsUnreadableTracks(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand("-l", lib, "playlist", "list")
	if err == nil || strings.Contains(out, "tracks:0") || strings.Contains(out, "No playlists") {
		t.Fatalf("unreadable tracks reported as empty: output=%q err=%v", out, err)
	}
}
