package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestRelocationRequiresJournalAndVacantDestination(t *testing.T) {
	ctx := context.Background()
	source, d, _ := fixture(t)
	d.Close()
	if err := RelocateLibrary(ctx, source, filepath.Join(t.TempDir(), "moved")); err == nil {
		t.Fatal("unrecorded move accepted")
	}
	destination := filepath.Join(t.TempDir(), "moved")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, source, "library.move", nil, func(ctx context.Context) error { return RelocateLibrary(ctx, source, destination) }); err == nil {
		t.Fatal("occupied destination overwritten")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatal("destination changed", err)
	}
	if _, err := os.Stat(filepath.Join(source, "Database2", "m.db")); err != nil {
		t.Fatal("failed move lost collection", err)
	}
}

func TestCopiedLibraryVerificationNeverConsumesChangedSources(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"media", "database", "missing-copy", "link", "missing-link", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			source, d, media := fixture(t)
			d.Close()
			destination := t.TempDir()
			lock := filepath.Join(source, ".engine-cli", "history", "lock.db")
			if err := copyLibraryTree(ctx, source, destination, lock); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "media":
				if err := os.WriteFile(media, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "database":
				c, err := db.Open(filepath.Join(source, "Database2", "m.db"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = c.Exec("CREATE TABLE concurrent_edit(id INTEGER)"); err != nil {
					t.Fatal(err)
				}
				c.Close()
			case "missing-copy":
				if err := os.Remove(filepath.Join(destination, filepath.Base(media))); err != nil {
					t.Fatal(err)
				}
			case "link", "missing-link":
				if err := os.Symlink("song.mp3", filepath.Join(source, "link")); err != nil {
					t.Skip(err)
				}
				if kind == "missing-link" {
					break
				}
				if err := os.Symlink("other.mp3", filepath.Join(destination, "link")); err != nil {
					t.Fatal(err)
				}
			}
			requestContext := ctx
			if kind == "cancelled" {
				var cancel context.CancelFunc
				requestContext, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := removeCopiedTree(requestContext, source, destination, lock, "active.json"); err == nil {
				t.Fatal("changed source was consumed")
			}
			if _, err := os.Stat(media); err != nil {
				t.Fatal("verification failure removed source", err)
			}
		})
	}
}

func TestLibraryCopyRejectsConflictsAndCancellation(t *testing.T) {
	ctx := context.Background()
	source, d, media := fixture(t)
	d.Close()
	if err := copyTreeFile(ctx, media, media); err == nil {
		t.Fatal("copy replaced original")
	}
	if err := copyTreeFile(ctx, t.TempDir(), filepath.Join(t.TempDir(), "file")); err == nil {
		t.Fatal("directory copied as file")
	}
	if err := copyTreeFile(ctx, filepath.Join(source, "missing"), filepath.Join(t.TempDir(), "file")); err == nil {
		t.Fatal("missing source accepted")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := copyLibraryTree(ctx, source, t.TempDir(), ""); err == nil {
		t.Fatal("cancelled library copy succeeded")
	}
}

func TestRelocationCancellationAndMissingCopyInputs(t *testing.T) {
	ctx := context.Background()
	source, d, _ := fixture(t)
	d.Close()
	if _, err := Run(ctx, source, "library.move", nil, func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return RelocateLibrary(ctx, source, filepath.Join(t.TempDir(), "moved"))
	}); err == nil {
		t.Fatal("cancelled relocation succeeded")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, source, "library.move", nil, func(ctx context.Context) error { return RelocateLibrary(ctx, source, filepath.Join(blocked, "moved")) }); err == nil {
		t.Fatal("invalid destination parent accepted")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := copyLibraryTree(ctx, missing, t.TempDir(), ""); err == nil {
		t.Fatal("missing copy source accepted")
	}
	if err := removeCopiedTree(ctx, missing, t.TempDir(), "", ""); err == nil {
		t.Fatal("missing move source accepted")
	}
	destination := filepath.Join(t.TempDir(), "moved")
	entry, err := Run(ctx, source, "library.move", nil, func(ctx context.Context) error { return RelocateLibrary(ctx, source, destination) })
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, destination, entry.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("force undo overwrote occupied original location")
	}
	if _, err = os.Stat(filepath.Join(destination, "Database2", "m.db")); err != nil {
		t.Fatal("conflict lost moved collection", err)
	}
}
