package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistPreparationRejectsChangedSourcesBeforeIntent(t *testing.T) {
	for _, kind := range []string{"deleted deletion entry", "lost historical image"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			deleted := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
			plan, err := planSmartlistRepair(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			active := filepath.Join(filepath.Dir(dir), "smartlist-repair")
			source := filepath.Join(lib, "Database2", "m.db")
			switch kind {
			case "deleted deletion entry":
				if err := os.Remove(filepath.Join(dir, deleted.ID+".json")); err != nil {
					t.Fatal(err)
				}
			case "lost historical image":
				if err := os.Remove(deleted.Before.Databases[source].Path); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan)
			if err == nil || prepared != nil {
				t.Fatalf("changed preparation accepted: %+v %v", prepared, err)
			}
			if _, err := os.Lstat(filepath.Join(active, smartlistJournalName)); !os.IsNotExist(err) {
				t.Fatalf("failed preparation enrolled pending intent: %v", err)
			}
		})
	}
}

func TestSmartlistRecoveryRetainsMalformedPendingEvidence(t *testing.T) {
	for _, kind := range []string{"wrong version"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;'`); err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistRepair(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := lock(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan)
			if closeErr := owner.Close(); err != nil || closeErr != nil {
				t.Fatalf("prepare: %v close: %v", err, closeErr)
			}
			if err := prepareSmartlistJournal(ctx, prepared.dir, prepared.journal); err != nil {
				t.Fatal(err)
			}
			pending := filepath.Join(prepared.dir, smartlistJournalName)
			if err := os.WriteFile(pending, []byte(`{"version":99}`), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(pending)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RecoverSmartlistRepair(ctx, lib)
			if result != nil || err == nil {
				t.Fatalf("unsafe recovery accepted: %+v %v", result, err)
			}
			after, err := os.ReadFile(pending)
			if err != nil || string(after) != string(before) {
				t.Fatalf("failed recovery discarded evidence: %q %v", after, err)
			}
		})
	}
}
