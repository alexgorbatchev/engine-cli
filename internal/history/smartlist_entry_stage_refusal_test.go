package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistHistoricalEntryStagingFailureKeepsOriginal(t *testing.T) {
	for _, kind := range []string{"unreadable original", "unwritable rollback directory", "blocked staged entry", "cancelled changeset"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(dir, entry.ID+".json")
			before, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			p := &smartlistPrepared{dir: t.TempDir()}
			switch kind {
			case "unreadable original":
				smartlistRestrict(t, original, 0)
			case "unwritable rollback directory":
				smartlistRestrict(t, p.dir, 0500)
			case "blocked staged entry":
				if err := os.Mkdir(filepath.Join(p.dir, entry.ID+".json"), 0700); err != nil {
					t.Fatal(err)
				}
			case "cancelled changeset":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := p.stageEntry(ctx, dir, entry, false); err == nil {
				t.Fatal("failed historical staging accepted")
			}
			if len(p.journal.Images) != 0 {
				t.Fatalf("failed staging enrolled a publication target: %+v", p.journal.Images)
			}
			if kind == "unreadable original" {
				if err := os.Chmod(original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if after, err := os.ReadFile(original); err != nil || string(after) != string(before) {
				t.Fatalf("failed staging changed original history: %q %v", after, err)
			}
			for _, side := range []*snapshot{entry.Before, entry.After} {
				for _, image := range side.Databases {
					if hash, err := fingerprint(context.Background(), image.Path); err != nil || hash != image.Hash {
						t.Fatalf("failed staging changed original snapshot: %q %v", hash, err)
					}
				}
			}
		})
	}
}
