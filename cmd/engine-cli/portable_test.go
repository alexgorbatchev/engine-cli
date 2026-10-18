package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func TestPortableBackupFailureStopsMutations(t *testing.T) {
	commands := [][]string{
		{"library", "sync"}, {"library", "prune"}, {"library", "restore", "missing"},
		{"playlist", "create", "Never"}, {"playlist", "rm", "1", "--force"}, {"playlist", "move", "1", "--to", "2"},
		{"playlist", "track", "add", "1", "1"}, {"playlist", "track", "rm", "1", "1"}, {"playlist", "track", "move", "1", "2", "1"},
		{"track", "add", "missing.mp3"}, {"track", "rm", "1"}, {"track", "move", "1", "missing.mp3"},
		{"track", "relocate", "--from", "old", "--to", "new"}, {"track", "artwork", "normalize"}, {"track", "upgrade", "1"},
	}
	for _, command := range commands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			lib := createTestLibrary(t)
			storage := filepath.Join(lib, ".engine-cli")
			if err := os.MkdirAll(storage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(storage, "backups"), []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-l", lib}, command...)
			_, err := executeCommand(args...)
			if err == nil || !strings.Contains(err.Error(), "backup") {
				t.Fatalf("expected backup failure: %v", err)
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			tr, err := db.GetTrackByID(context.Background(), d, 1)
			if err != nil || tr == nil {
				t.Fatalf("track changed despite backup failure: %v %v", tr, err)
			}
		})
	}
}
