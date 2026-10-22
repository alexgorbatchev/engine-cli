package history

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCollectsUnreferencedHistoryStorage(t *testing.T) {
	lib, _, song := fixture(t)
	ctx := context.Background()
	kept, err := Run(ctx, lib, "track.rm", ScopeFiles, []string{song}, func(context.Context) error { return os.Remove(song) })
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(lib, ".engine-cli", "history")
	objects := filepath.Join(dir, "objects")
	orphans := []string{
		filepath.Join(objects, strings.Repeat("ab", 32)),
		filepath.Join(objects, "pending-123"),
		filepath.Join(dir, entryTempPrefix+"123"),
		filepath.Join(dir, strings.Repeat("cd", entryIDBytes), "before", "m.db"),
	}
	unknown := []string{
		filepath.Join(dir, "notes", "keep"),
		filepath.Join(dir, strings.Repeat("ef", entryIDBytes-1), "keep"),
		filepath.Join(objects, "nested", "keep"),
	}
	for _, path := range append(orphans, unknown...) {
		writeFile(t, path)
	}
	if _, err = Run(ctx, lib, "track.update", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, path := range orphans {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("kept unreferenced %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, strings.Repeat("cd", entryIDBytes))); !os.IsNotExist(err) {
		t.Fatalf("kept orphaned work directory: %v", err)
	}
	for _, path := range unknown {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("removed storage it does not own: %v", err)
		}
	}
	if _, err = Undo(ctx, lib, kept.ID, UndoOptions{}); err != nil {
		t.Fatalf("collected content a retained entry needs: %v", err)
	}
	if data, err := os.ReadFile(song); err != nil || string(data) != "original audio" {
		t.Fatalf("restored %q err=%v", data, err)
	}
}

func TestRunStopsWhenUnreferencedStorageCannotBeRemoved(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	if _, err := Run(ctx, lib, "track.update", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(lib, ".engine-cli", "history", "objects")
	writeFile(t, filepath.Join(objects, "pending-123"))
	if err := os.Chmod(objects, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(objects, 0o700) })
	ran := false
	_, err := Run(ctx, lib, "track.update", ScopeFiles, nil, func(context.Context) error { ran = true; return nil })
	if ran || err == nil || !strings.Contains(err.Error(), "removing unreferenced operation history") {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
}

func TestCollectReportsUnreadableStorage(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{name: "missing history", setup: func(*testing.T, string) {}},
		{name: "objects is a file", setup: func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, "objects")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "history")
			tt.setup(t, dir)
			if err := collect(dir, nil); err == nil {
				t.Fatal("collected unreadable storage without an error")
			}
		})
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stored"), 0o600); err != nil {
		t.Fatal(err)
	}
}
