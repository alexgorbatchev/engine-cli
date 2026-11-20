package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A relative destination on the command line names a place under the shell's
// working directory, as shell paths do, not under the library folder.
func TestTrackMoveResolvesRelativeDestinationsFromTheWorkingDirectory(t *testing.T) {
	lib := createTestLibrary(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	work := t.TempDir()
	t.Chdir(work)
	if _, err := executeCommand("-l", lib, "--no-backup", "track", "move", "1", "sorted/"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "sorted", "track1.m4a")); err != nil {
		t.Fatalf("moved file not under the working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "sorted")); !os.IsNotExist(err) {
		t.Fatalf("relative destination resolved inside the library: %v", err)
	}
}
