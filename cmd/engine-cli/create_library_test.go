package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLICreateLibraryAtPath(t *testing.T) {
	root := newRootCommand()
	base := t.TempDir()
	t.Setenv("HOME", base)
	path := filepath.Join(base, "New collection")
	var out, diagnostics bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"library", "create", path, "--state-db", filepath.Join(base, "state.db"), "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("create: %v %s", err, &diagnostics)
	}
	var result struct {
		Path string `json:"path"`
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Path != "~/New collection" || result.UUID == "" {
		t.Fatalf("result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(path, "Database2", "m.db")); err != nil {
		t.Fatal(err)
	}
}
