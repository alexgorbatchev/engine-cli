package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSeedFailureRetainsPartialRootWithoutRetry(t *testing.T) {
	in := nativeBinary(t)
	g := capturedGolden(t)
	// The CLI is intact and hash-bound, but an invalid captured title must stop
	// before track import. The already-created native library is retained.
	v := g.Tracks[0].Values["title"]
	v.Data = "not-base64!"
	g.Tracks[0].Values["title"] = v
	root := filepath.Join(t.TempDir(), "partial")
	if err := seed(t.Context(), root, in, g); err == nil {
		t.Fatal("invalid title was seeded")
	}
	path := filepath.Join(root, "healthy", "Database2", "m.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed native seed did not retain its library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial seed published a complete manifest: %v", err)
	}
	if err := seed(t.Context(), root, in, capturedGolden(t)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("partial root was reused: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || digest(before) != digest(after) {
		t.Fatalf("refused retry changed retained native library: %v", err)
	}
}

func TestSeedRefusesChangedExecutableWithoutStartingIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "partial")
	path := filepath.Join(t.TempDir(), "engine-cli")
	if err := writeNew(path, []byte("changed executable")); err != nil {
		t.Fatal(err)
	}
	in := inputs{Binary: binding{Path: path, SHA256: digest([]byte("approved executable"))}}
	if err := seed(t.Context(), root, in, capturedGolden(t)); err == nil {
		t.Fatal("changed executable accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "inputs.json")); err != nil {
		t.Fatalf("partial inputs not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "healthy")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused binary created a library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "healthy-create.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexecuted binary published a command log: %v", err)
	}
}
