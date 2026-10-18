package artwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/progress"
)

func TestArtworkCancellationStagesCoverage(t *testing.T) {
	for _, phase := range []string{"artwork.database", "artwork.database.items", "artwork.cache", "artwork.cache.items", "artwork.prune.database", "artwork.prune.cache", "artwork.prune.cache.items"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			database, err := db.Open(filepath.Join(dir, "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := db.InitSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO AlbumArt (id,hash,albumArt) VALUES (1,'abc',X'0102')"); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(dir, "Artwork")
			if err := os.Mkdir(cache, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(cache, "orphan.jpg")
			if err := os.WriteFile(file, []byte("image"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reached := false
			ctx = progress.WithReporter(ctx, func(e progress.Event) {
				if e.Phase == phase {
					reached = true
					cancel()
				}
			})
			runner := func(context.Context, string, ...string) ([]byte, error) {
				return []byte(`{"streams":[{"width":1400,"height":1400}]}`), nil
			}
			if len(phase) >= 13 && phase[:13] == "artwork.prune" {
				_, err = PruneArtwork(ctx, database, cache)
			} else {
				_, err = NormalizeArtwork(ctx, database, dir, cache, runner)
			}
			if !reached || !errors.Is(err, context.Canceled) {
				t.Fatalf("reached=%v error=%v", reached, err)
			}
			if _, err := os.Stat(file); err != nil {
				t.Fatalf("cancelled stage deleted cache file: %v", err)
			}
		})
	}
}
