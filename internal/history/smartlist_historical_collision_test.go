package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistHistoricalStageOwnsOnlySelectedPredecessors(t *testing.T) {
	for _, joining := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove owned collision member", true: "refuse joining unrelated collision"}[joining], func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			// Native nullable successor columns permit these damaged duplicate
			// links without removing Engine's uniqueness constraints.
			if _, err := database.Exec(`DELETE FROM Smartlist;
				INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
				VALUES ('a','Owned','House;','Sets;',NULL,'rules-a'),
				('b','Unrelated B','House;','Sets;',NULL,'rules-b'),
				('c','Unrelated C','House;','Sets;',NULL,'rules-c')`); err != nil {
				t.Fatal(err)
			}
			if joining {
				if _, err := database.Exec(`UPDATE Smartlist SET nextPlaylistPath='Missing;' WHERE listUuid='a'`); err != nil {
					t.Fatal(err)
				}
			}
			source := filepath.Join(lib, "Database2", "m.db")
			before, hash, err := readSmartlistVersion(ctx, source, "")
			if err != nil {
				t.Fatal(err)
			}
			patch := smartlistPatch{before: before.Saved[0]}
			if patch.before.UUID != "a" {
				t.Fatal("fixture selected wrong identity")
			}
			if joining {
				after := patch.before
				after.NextPath = "Sets;"
				patch.after = &after
			}
			destination := filepath.Join(t.TempDir(), "historical.db")
			amendment := smartlistHistoricalAmendment{entryID: "owned-delete", image: databaseState{Path: source, Hash: hash}, patches: []smartlistPatch{patch}}
			result, err := stageHistoricalSmartlistImage(ctx, destination, amendment)
			if joining {
				if err == nil || result != "" {
					t.Fatalf("amendment joined unrelated predecessor collision: %q %v", result, err)
				}
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatalf("rejected stage survived: %v", err)
				}
			} else {
				if err != nil || result == "" {
					t.Fatalf("owned collision removal refused: %q %v", result, err)
				}
				staged, err := db.Open(destination, true)
				if err != nil {
					t.Fatal(err)
				}
				defer staged.Close()
				var count int
				if err := staged.QueryRow(`SELECT count(*) FROM Smartlist WHERE listUuid IN ('b','c') AND parentPlaylistPath='House;' AND nextPlaylistPath='Sets;' AND nextListUuid IS NULL AND rules='rules-'||listUuid`).Scan(&count); err != nil || count != 2 {
					t.Fatalf("unrelated predecessor rows changed: %d %v", count, err)
				}
			}
			if got, err := fingerprint(ctx, source); err != nil || got != hash {
				t.Fatalf("historical original changed: %s %v", got, err)
			}
		})
	}
}
