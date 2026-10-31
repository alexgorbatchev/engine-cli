package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistReferencedImagesValidatedBeforeAnyRollback(t *testing.T) {
	for _, kind := range []string{"original", "corrected", "outside", "alias", "missing hash"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistRepair(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareSmartlistJournal(ctx, prepared.dir, prepared.journal); err != nil {
				t.Fatal(err)
			}
			for _, image := range prepared.journal.Images {
				if err := writeSmartlistTarget(ctx, lib, prepared.dir, image, false); err != nil {
					t.Fatal(err)
				}
			}
			first := filepath.Join(lib, prepared.journal.Images[0].Target)
			published, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			var reference *smartlistJournalReference
			for i := range prepared.journal.References {
				candidate := &prepared.journal.References[i]
				corrected := filepath.Dir(filepath.Dir(candidate.Path)) == filepath.Join(".engine-cli", "smartlist-repairs")
				if (kind == "corrected") == corrected {
					reference = candidate
					break
				}
			}
			if reference == nil {
				t.Fatal("native repair did not retain reference class")
			}
			path := filepath.Join(lib, reference.Path)
			switch kind {
			case "original", "corrected":
				if err := os.WriteFile(path, []byte("damaged referenced database"), 0600); err != nil {
					t.Fatal(err)
				}
			case "outside":
				reference.Path = filepath.Join("..", "outside.db")
			case "missing hash":
				reference.Hash = ""
			case "alias":
				retained := filepath.Join(t.TempDir(), "retained.db")
				if err := copyDatabase(ctx, path, retained); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(retained, path); err != nil {
					t.Skipf("native symlink unavailable: %v", err)
				}
			}
			if err := restoreSmartlistTargets(ctx, lib, prepared.dir, prepared.journal); err == nil {
				t.Fatal("damaged referenced image granted rollback")
			}
			after, err := os.ReadFile(first)
			if err != nil || string(after) != string(published) {
				t.Fatalf("history restored before reference validation: %v", err)
			}
		})
	}
}
