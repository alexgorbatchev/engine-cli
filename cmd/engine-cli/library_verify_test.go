package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestLibraryVerifyIncompleteRead(t *testing.T) {
	for _, mode := range []string{"human", "agent", "json"} {
		t.Run(mode, func(t *testing.T) {
			lib := createTestLibrary(t)
			t.Setenv("AGENT", "0")
			args := []string{"-l", lib, "library", "verify"}
			if mode == "agent" {
				t.Setenv("AGENT", "1")
			}
			if mode == "json" {
				args = append(args, "--json")
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE PlaylistEntity SET nextEntityId=id`); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			output, err := executeCommand(args...)
			if err == nil || progress.ErrorCode(err) != "library_verify_incomplete" {
				t.Fatalf("incomplete audit output=%q error=%v", output, err)
			}
			for _, success := range []string{"Library Verification Report", "status: healthy", `"isHealthy"`} {
				if strings.Contains(output, success) {
					t.Fatalf("incomplete audit emitted report: %q", output)
				}
			}
		})
	}
}
