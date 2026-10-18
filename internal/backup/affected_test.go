package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/history"
)

func TestArtworkSnapshotBoundaries(t *testing.T) {
	ctx := context.Background()
	src, dst := t.TempDir(), t.TempDir()
	blocked := filepath.Join(dst, "blocked")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyArtwork(ctx, blocked, dst); err == nil {
		t.Fatal("accepted artwork file as source directory")
	}
	if _, _, err := copyArtwork(ctx, src, blocked); err == nil {
		t.Fatal("accepted blocked artwork destination")
	}
	if err := os.Mkdir(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	count, _, err := copyArtwork(ctx, src, dst)
	if err != nil || count != 0 {
		t.Fatalf("copied nested data: %d %v", count, err)
	}
}

func TestAutomaticBackupExcludesUnchangedArtwork(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	lib := createDummyEngineLibrary(t)
	info, err := AutoBackupIfNeeded(context.Background(), lib, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.ArtFiles != 0 {
		t.Fatal("automatic snapshot copied artwork")
	}
	if _, err = os.Stat(filepath.Join(info.Path, "Artwork")); !os.IsNotExist(err) {
		t.Fatalf("artwork copied: %v", err)
	}
}

func TestRestorePreservesOnlyOverwrittenArtwork(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	lib := createDummyEngineLibrary(t)
	ctx := context.Background()
	backup, err := CreateBackup(ctx, lib, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(lib, "Artwork", "sample_art.jpg")
	if err = os.WriteFile(changed, []byte("current artwork"), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(lib, "Artwork", "untouched.jpg")
	if err = os.WriteFile(unrelated, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := history.Run(ctx, lib, "library.restore", nil, func(ctx context.Context) error { return RestoreBackup(ctx, backup.Path, lib) })
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Before.Files) != 1 {
		t.Fatalf("captured %v", e.Before.Files)
	}
	if _, err = history.Undo(ctx, lib, e.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(changed)
	if err != nil || string(got) != "current artwork" {
		t.Fatalf("lost artwork %q %v", got, err)
	}
}

func TestRestoreRefusesUnpreservableArtwork(t *testing.T) {
	for _, recording := range []bool{false, true} {
		t.Run(map[bool]string{false: "copy failure", true: "preserve failure"}[recording], func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			lib := createDummyEngineLibrary(t)
			ctx := context.Background()
			b, err := CreateBackup(ctx, lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lib, "Artwork", "sample_art.jpg")
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			restore := func(ctx context.Context) error { return RestoreBackup(ctx, b.Path, lib) }
			if recording {
				_, err = history.Run(ctx, lib, "restore", nil, restore)
			} else {
				err = restore(ctx)
			}
			if err == nil {
				t.Fatal("silently skipped failed artwork restoration")
			}
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				t.Fatalf("changed blocked destination: %v %v", info, err)
			}
		})
	}
}
