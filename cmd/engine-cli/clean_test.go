package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCleanPreservesSessionScratch(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("../../justfile")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "justfile"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	outputs := []string{"bin/app", "dist/archive", "coverage.out"}
	for _, name := range append(outputs, ".tmp/another-session/keep") {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("just", "clean")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clean: %v\n%s", err, out)
	}
	for _, name := range outputs {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("clean left %s: %v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".tmp/another-session/keep")); err != nil || string(data) != "sentinel" {
		t.Fatalf("clean damaged another session's scratch: %q, %v", data, err)
	}
}
