package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestIntakeCLISyncInvalidInputDoesNotEnrollLifetime(t *testing.T) {
	for _, name := range []string{"missing paths", "empty destination", "same library", "invalid conflict"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			t.Chdir(directory)
			source := transferLibrary(t, "source")
			destination := transferLibrary(t, "destination")
			args := []string{"library", "sync", "--root", "--no-backup"}
			switch name {
			case "empty destination":
				args = append(args, "--from", source, "--to", "")
			case "same library":
				args = append(args, "--from", source, "--to", source)
			case "invalid conflict":
				args = append(args, "--from", source, "--to", destination, "--conflict", "invalid")
			}
			if out, err := executeIntakeCLI(args...); err == nil {
				t.Fatalf("invalid sync succeeded: %s", out)
			}
			for _, library := range []string{directory, source, destination} {
				path := filepath.Join(filepath.Dir(library), ".engine-cli-intake", filepath.Base(library))
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid sync enrolled lifetime storage %s: %v", path, err)
				}
			}
		})
	}
}

func TestIntakeCLILifetimeExcludesNativeReplacement(t *testing.T) {
	for _, action := range []string{"restore", "undo", "sync"} {
		t.Run(action, func(t *testing.T) {
			library := intakeCLILibrary(t)
			var args []string
			switch action {
			case "restore":
				snapshot, err := backup.CreateBackup(t.Context(), library, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				args = []string{"-l", library, "--no-backup", "library", "restore", snapshot.Path}
			case "undo":
				if out, err := executeIntakeCLI("-l", library, "--no-backup", "playlist", "create", "Native Undo target"); err != nil {
					t.Fatalf("create real Undo entry: %s %v", out, err)
				}
				args = []string{"-l", library, "--no-backup", "library", "undo"}
			case "sync":
				source := transferLibrary(t, "source")
				args = []string{"library", "sync", "--from", source, "--to", library, "--root", "--no-backup"}
			}
			queue, err := intake.Open(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := queue.Close(); err != nil {
					t.Error(err)
				}
			})
			before, err := history.List(library)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := executeIntakeCLI(args...); progress.ErrorCode(err) != "intake_replacing" {
				t.Fatalf("native %s bypassed live queue: %s %v", action, out, err)
			}
			after, err := history.List(library)
			if err != nil || len(after) != len(before) {
				t.Fatalf("refused native replacement created History: %v %v", after, err)
			}
			if err := queue.Close(); err != nil {
				t.Fatal(err)
			}
			if out, err := executeIntakeCLI(args...); err != nil {
				t.Fatalf("native %s remained blocked after Store.Close: %s %v", action, out, err)
			}
		})
	}
}
