package artwork

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestArtworkHistoryPreservesOnlyChangedCacheFiles(t *testing.T) {
	for _, operation := range []string{"prune", "rewrite", "prune preservation failure", "rewrite preservation failure"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			lib := t.TempDir()
			ctx := context.Background()
			if err := os.Mkdir(filepath.Join(lib, "Database2"), 0700); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if err = db.InitSchema(ctx, d); err != nil {
				t.Fatal(err)
			}
			if _, err = d.Exec("INSERT INTO AlbumArt(id,hash,albumArt) VALUES(1,'retained',x'01');INSERT INTO Track(id,path,albumArtId) VALUES(1,'song.mp3',1)"); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(lib, "Artwork")
			if err = os.Mkdir(cache, 0700); err != nil {
				t.Fatal(err)
			}
			changed := filepath.Join(cache, "orphan.jpg")
			retained := filepath.Join(cache, "retained.jpg")
			for _, p := range []string{changed, retained} {
				if err = os.WriteFile(p, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			failure := operation == "prune preservation failure" || operation == "rewrite preservation failure"
			if failure {
				if err = os.Remove(changed); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(retained, changed); err != nil {
					t.Fatal(err)
				}
			}
			e, err := history.Run(ctx, lib, operation, nil, func(ctx context.Context) error {
				if operation == "prune" || operation == "prune preservation failure" {
					_, err := PruneArtwork(ctx, d, cache)
					return err
				}
				return writeCache(ctx, []byte("changed"), changed)
			})
			if failure {
				if err == nil {
					t.Fatal("mutation ignored preservation failure")
				}
				data, err := os.ReadFile(retained)
				if err != nil || string(data) != "original" {
					t.Fatalf("changed linked file %q: %v", data, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(e.Before.Files) != 1 {
				t.Fatalf("captured unrelated artwork: %v", e.Before.Files)
			}
			if _, err = history.Undo(ctx, lib, e.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(changed)
			if err != nil || string(got) != "original" {
				t.Fatalf("lost artwork %q %v", got, err)
			}
		})
	}
}

func TestCacheWriteFailure(t *testing.T) {
	path := t.TempDir()
	if err := writeCache(context.Background(), []byte("art"), path); err == nil {
		t.Fatal("ignored cache write failure")
	}
}
