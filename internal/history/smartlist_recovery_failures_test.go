package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistRecoveryRejectsUntrustedStateBeforeAnyRestore(t *testing.T) {
	for _, damage := range []string{"invalid journal", "foreign library", "missing original", "changed target", "unreadable native schema"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			lib, database, prepared := partiallyPublishedSmartlistRepair(t)
			first := prepared.journal.Images[0]
			pending := filepath.Join(prepared.dir, smartlistJournalName)
			var err error
			switch damage {
			case "invalid journal":
				err = os.WriteFile(pending, []byte("{damaged"), 0600)
			case "foreign library":
				journal := prepared.journal
				journal.Library = "not-the-current-library"
				var data []byte
				data, err = json.Marshal(journal)
				if err == nil {
					err = os.WriteFile(pending, data, 0600)
				}
			case "missing original":
				err = os.Remove(filepath.Join(prepared.dir, prepared.journal.Images[1].Original))
			case "changed target":
				_, err = database.Exec(`UPDATE Playlist SET title='External' WHERE id=2`)
			case "unreadable native schema":
				_, err = database.Exec(`DROP TABLE Playlist`)
			}
			if err != nil {
				t.Fatal(err)
			}
			entryPath := filepath.Join(lib, first.Target)
			entryBefore, err := os.ReadFile(entryPath)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(lib, "Database2", "m.db")
			nativeBefore, err := fingerprint(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RecoverSmartlistRepair(ctx, lib)
			if result != nil || progress.ErrorCode(err) != "smartlist_repair_pending" {
				t.Fatalf("untrusted recovery returned %+v, %v", result, err)
			}
			entryAfter, err := os.ReadFile(entryPath)
			if err != nil || string(entryAfter) != string(entryBefore) {
				t.Fatalf("history target changed before all evidence validated: %v", err)
			}
			if got, err := fingerprint(ctx, target); err != nil || got != nativeBefore {
				t.Fatalf("native target changed on refused recovery: %s %v", got, err)
			}
			if _, err := os.Lstat(pending); err != nil {
				t.Fatalf("refused recovery discarded pending evidence: %v", err)
			}
		})
	}
}

func TestSmartlistRecoveryRetainsIntentUntilRollbackMarkerIsDurable(t *testing.T) {
	ctx := context.Background()
	lib, _, prepared := partiallyPublishedSmartlistRepair(t)
	terminal := filepath.Join(prepared.dir, "rolled-back.json")
	if err := os.Mkdir(terminal, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverSmartlistRepair(ctx, lib)
	if result != nil || progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("failed terminal publication reported success: %+v %v", result, err)
	}
	// Native targets were restored, but reads must remain blocked until the
	// terminal publication succeeds. Retrying must not require partial state.
	if err := validateSmartlistTargets(ctx, lib, prepared.dir, prepared.journal, false); err != nil {
		t.Fatalf("original targets not restored before marker failure: %v", err)
	}
	if _, err := List(lib); progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("unfinished rollback exposed history: %v", err)
	}
	if err := os.Remove(terminal); err != nil {
		t.Fatal(err)
	}
	result, err = RecoverSmartlistRepair(ctx, lib)
	if err != nil || result == nil || !result.RolledBack || result.RestoredTargets != len(prepared.journal.Images) {
		t.Fatalf("rollback retry: %+v %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(prepared.dir, smartlistJournalName)); !os.IsNotExist(err) {
		t.Fatalf("successful rollback retained pending marker: %v", err)
	}
	if result, err := RecoverSmartlistRepair(ctx, lib); err != nil || result == nil || result.RolledBack {
		t.Fatalf("terminal recovery was not idempotent: %+v %v", result, err)
	}
}

func partiallyPublishedSmartlistRepair(t *testing.T) (string, *sql.DB, *smartlistPrepared) {
	t.Helper()
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		}
	})
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
	if err := writeSmartlistTarget(ctx, lib, prepared.dir, prepared.journal.Images[0], false); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return lib, database, prepared
}
