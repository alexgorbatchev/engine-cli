package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistIndependentReplayRespectsSavedRowIntroduction(t *testing.T) {
	for _, tc := range []struct {
		name, finalMove, wantNext string
	}{
		{"healthy anchor after old round trip", "", "House;"},
		{"broken anchor after later departure", `UPDATE Playlist SET parentListId=3,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=3 WHERE id=2`, "Third;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if _, err := database.Exec(`DELETE FROM Smartlist`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.CreatePlaylist(ctx, database, "Third", 0, false); err != nil {
				t.Fatal(err)
			}
			recordSmartlistMutation(t, lib, database, "playlist.move", `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1`)
			recordSmartlistMutation(t, lib, database, "playlist.move", `UPDATE Playlist SET nextListId=NULL WHERE id=2; UPDATE Playlist SET parentListId=0,nextListId=3 WHERE id=1; UPDATE Playlist SET nextListId=1 WHERE id=2`)
			if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
				('late','Late saved row','','House;','','opaque-late'),
				('unrelated','Unrelated damage','Missing;','','','opaque-unrelated')`); err != nil {
				t.Fatal(err)
			}
			if tc.finalMove != "" {
				recordSmartlistMutation(t, lib, database, "playlist.move", tc.finalMove)
			}
			evidence, err := readSmartlistEvidence(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			wantVersions := 4
			if tc.finalMove != "" {
				wantVersions = 6
			}
			if len(evidence.versions) != wantVersions {
				t.Fatalf("unexpected historical image count: %d", len(evidence.versions))
			}
			for i, version := range evidence.versions {
				present := false
				for _, row := range version.inventory.Saved {
					present = present || row.UUID == "late"
				}
				if present != (i >= 4) {
					t.Fatalf("wrong saved-row temporal evidence at image %d: present=%t", i, present)
				}
			}
			lateBroken := false
			for _, issue := range evidence.current.Issues {
				lateBroken = lateBroken || issue.UUID == "late"
			}
			if lateBroken != (tc.finalMove != "") {
				t.Fatalf("unexpected current saved-row validity: issues=%+v", evidence.current.Issues)
			}
			orders, err := readSmartlistOrderEvidence(ctx, lib, evidence)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistLinks(ctx, evidence, orders)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(lib, "Database2", "m.db")
			if current, err := fingerprint(ctx, source); err != nil || current != evidence.hash {
				t.Fatalf("planning changed source: %q %v", current, err)
			}
			var got *db.SmartlistRecord
			for _, row := range evidence.current.Saved {
				if row.UUID == "late" {
					copy := row
					got = &copy
				}
			}
			for _, patch := range plan.patches {
				if patch.before.UUID == "late" {
					got = patch.after
				}
			}
			if got == nil || got.ParentPath != "" || got.NextPath != tc.wantNext || got.NextUUID != "" {
				t.Fatalf("replayed departure predating saved row: want root -> %q; got %+v; patches=%+v", tc.wantNext, got, plan.patches)
			}
			if tc.finalMove == "" {
				for _, patch := range plan.patches {
					if patch.before.UUID == "late" {
						t.Fatalf("healthy saved row was patched: %+v", patch)
					}
				}
			}
		})
	}
}
