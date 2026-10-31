package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestSmartlistPublicationFailureRollsBackNativeTargets(t *testing.T) {
	for _, failure := range []string{"cancelled after entry", "destination busy"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
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
			publication, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if failure == "cancelled after entry" {
				if err := writeSmartlistTarget(ctx, lib, prepared.dir, prepared.journal.Images[0], false); err != nil {
					t.Fatal(err)
				}
				cancel()
			} else {
				locked, err := open(ctx, filepath.Join(lib, "Database2", "m.db"), sqlite.OpenReadWrite)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := locked.Close(); err != nil {
						t.Error(err)
					}
				}()
				if err := sqlitex.ExecuteTransient(locked, "BEGIN IMMEDIATE", nil); err != nil {
					t.Fatal(err)
				}
				if err := publishSmartlistTargets(publication, lib, prepared); err == nil {
					t.Fatal("native destination lock permitted publication")
				}
				// Release the real destination writer before rollback can own it.
				if err := sqlitex.ExecuteTransient(locked, "ROLLBACK", nil); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "cancelled after entry" {
				if err := publishSmartlistTargets(publication, lib, prepared); err == nil {
					t.Fatal("cancelled publication succeeded")
				}
			}
			first := prepared.journal.Images[0]
			if hash, err := smartlistJournalHash(ctx, filepath.Join(lib, first.Target), false); err != nil || hash != first.StagedHash {
				t.Fatalf("failure did not follow real first publication: %s %v", hash, err)
			}
			if _, err := os.Stat(filepath.Join(prepared.dir, "committed.json")); !os.IsNotExist(err) {
				t.Fatalf("failed publication became committed: %v", err)
			}
			if err := rollbackSmartlistRepair(context.WithoutCancel(publication), lib, prepared.dir, prepared.journal); err != nil {
				t.Fatal(err)
			}
			if err := validateSmartlistTargets(ctx, lib, prepared.dir, prepared.journal, false); err != nil {
				t.Fatalf("rollback did not restore every original: %v", err)
			}
			if _, err := os.Stat(filepath.Join(prepared.dir, "rolled-back.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
