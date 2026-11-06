package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistReplayRejectsLateSuccessorEvidence(t *testing.T) {
	for _, predecessor := range []bool{false, true} {
		name := "late tail"
		if predecessor {
			name = "late predecessor"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if _, err := database.Exec(`DELETE FROM Smartlist;
				INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
				VALUES ('p','Original predecessor','','House;','','original-rule')`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.CreatePlaylist(ctx, database, "Third", 0, false); err != nil {
				t.Fatal(err)
			}
			if predecessor {
				if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
					VALUES ('tail','Original tail','','Sets;','','tail-rule')`); err != nil {
					t.Fatal(err)
				}
			}
			recordSmartlistMutation(t, lib, database, "playlist.move", `UPDATE Playlist SET parentListId=3,nextListId=0 WHERE id=1`)
			nextPath, nextUUID := "Sets;", ""
			if predecessor {
				nextPath, nextUUID = "", "tail"
			}
			if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
				VALUES ('late','Later saved row','',?,?, 'late-rule')`, nextPath, nextUUID); err != nil {
				t.Fatal(err)
			}
			evidence, err := readSmartlistEvidence(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistLinks(ctx, evidence, orders)
			if err != nil {
				t.Fatal(err)
			}
			// No snapshot proves where the newly introduced chain belongs relative
			// to p. Remove only the unresolved broken row; do not invent a splice
			// through the later chain or remove that healthy current chain.
			if len(plan.patches) != 1 || plan.patches[0].before.UUID != "p" || plan.patches[0].after != nil || len(plan.deletions) != 0 {
				t.Fatalf("unproven temporal splice: %+v", plan)
			}
			source := filepath.Join(lib, "Database2", "m.db")
			if _, err := stageSmartlistImage(ctx, source, filepath.Join(t.TempDir(), "repaired.db"), evidence.hash, plan.patches); err != nil {
				t.Fatal(err)
			}
			if actual, err := fingerprint(ctx, source); err != nil || actual != evidence.hash {
				t.Fatalf("planning or staging changed live source: %q %v", actual, err)
			}
		})
	}
}
