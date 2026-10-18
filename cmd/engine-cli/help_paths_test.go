package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpFoldsHomeWithoutChangingLibraryPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "dj")
	t.Setenv("HOME", home)
	t.Setenv("COLUMNS", "240")
	for _, mode := range []string{"0", "1", "true", "yes"} {
		for _, args := range [][]string{{"api", "serve", "--help"}, {"library", "verify", "--help"}, {"track", "upgrade", "--help"}} {
			t.Run(mode+"/"+strings.Join(args, " "), func(t *testing.T) {
				t.Setenv("AGENT", mode)
				cmd := newRootCommand()
				var output bytes.Buffer
				cmd.SetOut(&output)
				cmd.SetErr(&output)
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(output.String(), home) || !strings.Contains(output.String(), "~/Music/Engine Library") {
					t.Fatalf("home path was not folded: %s", output.String())
				}
				want := filepath.Join(home, "Music", "Engine Library")
				if libDirFlag != want {
					t.Fatalf("execution path changed to %q, want %q", libDirFlag, want)
				}
			})
		}
	}
}
