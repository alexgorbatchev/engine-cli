package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/discovery"
	"github.com/spf13/cobra"
)

func TestLibraryDiscover(t *testing.T) {
	lib := createTestLibrary(t)
	lib, err := filepath.EvalSymlinks(lib)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			var stdout bytes.Buffer
			cmd := newRootCommand()
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"-l", filepath.Join(t.TempDir(), "not-selected"), "library", "discover", lib, "--json"})
			err := cmd.Execute()
			out := stdout.String()
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Libraries []struct {
					Path string `json:"path"`
				} `json:"libraries"`
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Libraries) != 1 || report.Libraries[0].Path != displayPath(lib) {
				t.Fatalf("result %s", out)
			}
			out, err = executeCommand("library", "discover", lib)
			if err != nil || !strings.Contains(out, displayPath(lib)) {
				t.Fatalf("text %s: %v", out, err)
			}
		})
	}
}

func TestDiscoveryEmptyWarningsAndOutputFailure(t *testing.T) {
	t.Setenv("AGENT", "0")
	out, err := executeCommand("library", "discover", t.TempDir())
	if err != nil || !strings.Contains(out, "No Engine DJ libraries found") {
		t.Fatalf("empty output %q: %v", out, err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Database2", "m.db"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = executeCommand("library", "discover", root)
	if err != nil || !strings.Contains(out, "warning:") {
		t.Fatalf("warning output %q: %v", out, err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		for _, kind := range []string{"empty", "library", "warning", "json"} {
			c := &cobra.Command{}
			c.SetOut(upgradeFailWriter{})
			c.SetErr(upgradeFailWriter{})
			r := &discovery.Report{}
			if kind == "library" {
				r.Libraries = []discovery.Library{{Path: root, Name: "Folder", Source: "folder"}}
			}
			if kind == "warning" {
				r.Libraries = []discovery.Library{{Path: root}}
				r.Warnings = []discovery.Warning{{Path: root, Message: "unreadable"}}
				c.SetOut(io.Discard)
			}
			if err := printDiscovery(c, r, kind == "json"); err == nil {
				t.Fatalf("ignored %s output failure in mode %s", kind, mode)
			}
		}
	}
}
