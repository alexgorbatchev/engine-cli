package history

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLibraryRelocationAcrossFilesystemsAndUndo(t *testing.T) {
	if info, err := os.Stat("/dev/shm"); err != nil || !info.IsDir() {
		t.Skip("separate temporary filesystem is unavailable")
	}
	source, d, media := fixture(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("song.mp3", filepath.Join(source, "audio-link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "/dev/shm")
	t.Run("separate filesystem", func(t *testing.T) {
		parent := t.TempDir()
		destination := filepath.Join(parent, "Library")
		a, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.Stat(parent)
		if err != nil {
			t.Fatal(err)
		}
		if a.Sys().(*syscall.Stat_t).Dev == b.Sys().(*syscall.Stat_t).Dev {
			t.Skip("temporary roots use the same filesystem")
		}
		ctx := context.Background()
		entry, err := Run(ctx, source, "library.move", ScopeCollection, nil, func(ctx context.Context) error { return RelocateLibrary(ctx, source, destination) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(source); !os.IsNotExist(err) {
			t.Fatalf("source remnants: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(destination, filepath.Base(media)))
		if err != nil || string(got) != "original audio" {
			t.Fatalf("media changed: %s %v", got, err)
		}
		link, err := os.Readlink(filepath.Join(destination, "audio-link"))
		if err != nil || link != "song.mp3" {
			t.Fatalf("link changed: %s %v", link, err)
		}
		if _, err = Undo(ctx, destination, entry.ID, UndoOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(media); err != nil {
			t.Fatalf("undo lost original media: %v", err)
		}
		if _, err = os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("undo retained destination: %v", err)
		}
	})
}
