package history

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSmartlistAmendedDeleteSelectiveUndo(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "unrelated later edit and archived repair"
		if conflict {
			name = "reused saved identity conflicts"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			deleted := recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
			review, err := PreviewSmartlistRepair(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken); err != nil || result == nil || result.AmendedDeletes != 1 {
				t.Fatalf("amend original deletion: %+v %v", result, err)
			}
			all, err := List(lib)
			if err != nil || len(all) != 1 {
				t.Fatalf("amended inventory: %+v %v", all, err)
			}
			savedEntity := false
			for _, entity := range all[0].Entities {
				savedEntity = savedEntity || entity.Table == "Smartlist"
			}
			if !savedEntity {
				t.Fatal("amended deletion omitted its saved-row entity")
			}
			if conflict {
				recordSmartlistMutation(t, lib, database, "smartlist.create", `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','New saved identity','Sets;','','','new rules')`)
			} else {
				// A later repair archives its active journal. Original deletion Undo
				// must still resolve the immutable corrected historical images.
				if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('unknown','Unknown','Missing;','','','opaque')`); err != nil {
					t.Fatal(err)
				}
				review, err = PreviewSmartlistRepair(ctx, lib)
				if err != nil {
					t.Fatal(err)
				}
				if result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken); err != nil || result == nil || result.Removed != 1 {
					t.Fatalf("second repair: %+v %v", result, err)
				}
				recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Later Sets' WHERE id=2`)
			}
			target := filepath.Join(lib, "Database2", "m.db")
			before, err := fingerprint(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			_, previewErr := Undo(ctx, lib, deleted.ID, UndoOptions{DryRun: true})
			if (previewErr != nil) != conflict {
				t.Fatalf("selective preview conflict=%v: %v", conflict, previewErr)
			}
			if hash, err := fingerprint(ctx, target); err != nil || hash != before {
				t.Fatalf("preview changed native rows: %s %v", hash, err)
			}
			_, err = Undo(ctx, lib, deleted.ID, UndoOptions{})
			if conflict {
				if err == nil {
					t.Fatal("selective Undo overwrote a reused saved identity")
				}
				if hash, err := fingerprint(ctx, target); err != nil || hash != before {
					t.Fatalf("refused Undo changed native rows: %s %v", hash, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			current, _, err := readSmartlistVersion(ctx, target, "")
			if err != nil || len(current.Issues) != 0 || len(current.Saved) != 1 || current.Saved[0].UUID != "saved" || current.Saved[0].ParentPath != "House;" {
				t.Fatalf("selective Undo restored wrong saved rows: %+v %v", current, err)
			}
			var title string
			if err := database.QueryRow(`SELECT title FROM Playlist WHERE id=2`).Scan(&title); err != nil || title != "Later Sets" {
				t.Fatalf("selective Undo lost later ordinary edit: %q %v", title, err)
			}
		})
	}
}
