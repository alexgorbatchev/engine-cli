package artwork

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestOperationProgressAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "progress", true: "cancel"}[cancelEarly], func(t *testing.T) {
			dir := t.TempDir()
			database, err := db.Open(filepath.Join(dir, "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := db.InitSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO Track(id,path) VALUES(1,'missing.mp3'),(2,'other.mp3')"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []progress.Event
			ctx = progress.WithReporter(ctx, func(e progress.Event) {
				events = append(events, e)
				if cancelEarly {
					cancel()
				}
			})
			_, err = NormalizeArtwork(ctx, database, dir, filepath.Join(dir, "Artwork"))
			if cancelEarly && err == nil {
				t.Fatal("cancellation ignored")
			}
			if !cancelEarly && err != nil {
				t.Fatal(err)
			}
			if len(events) == 0 {
				t.Fatal("missing operation progress")
			}
			previous := 0.0
			for _, e := range events {
				if e.Percent < previous || e.Percent >= 100 {
					t.Fatalf("invalid operation progress: %+v", events)
				}
				previous = e.Percent
			}
		})
	}
}

func TestPruneCancellationPreservesArtwork(t *testing.T) {
	for _, phase := range []string{"artwork.prune", "artwork.prune.database", "artwork.prune.cache"} {
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
			if _, err := database.Exec("INSERT INTO AlbumArt(id,hash,albumArt) VALUES(1,'orphan',X'01')"); err != nil {
				t.Fatal(err)
			}
			art := filepath.Join(dir, "orphan.jpg")
			if err := os.WriteFile(art, []byte("artwork"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = progress.WithReporter(ctx, func(e progress.Event) {
				if e.Phase == phase {
					cancel()
				}
			})
			if _, err := PruneArtwork(ctx, database, dir); err == nil {
				t.Fatal("cancellation ignored")
			}
			if _, err := os.Stat(art); err != nil {
				t.Fatal("removed artwork after cancellation")
			}
		})
	}
}
