package history

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistHistoricalStagePreservesUnrelatedDamage(t *testing.T) {
	for _, tc := range []struct {
		name, unrelated string
	}{
		{"shared ordinary anchor", `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
			VALUES ('u','Unrelated missing parent','Missing;','Sets;','','opaque-u')`},
		{"independent directed cycle", `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
			VALUES ('c','Cycle C','House;','','d','opaque-c'),('d','Cycle D','House;','','c','opaque-d')`},
		{"unrelated terminal orphan", `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
			VALUES ('u','Unrelated missing parent','Missing;','','','opaque-u')`},
		{"mixed unrelated damage", `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
			VALUES ('u','Unrelated missing parent','Missing;','Sets;','','opaque-u'),
			('c','Cycle C','House;','','d','opaque-c'),('d','Cycle D','House;','','c','opaque-d')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if _, err := database.Exec(`DELETE FROM Smartlist;
				ALTER TABLE Smartlist ADD COLUMN futureData BLOB;
				ALTER TABLE Smartlist ADD COLUMN nullableValue TEXT;
				INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,futureData)
				VALUES ('a','Selected deletion-time row','Old;','Sets;','','{"nested":["λ",null]}',x'00ff80');` + tc.unrelated + `;
				UPDATE Smartlist SET futureData=x'ff0080' WHERE listUuid!='a'`); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(lib, "Database2", "m.db")
			inventory, hash, err := readSmartlistVersion(ctx, source, "")
			if err != nil {
				t.Fatal(err)
			}
			columns, original, err := readCompleteSmartlistRows(ctx, database)
			if err != nil {
				t.Fatal(err)
			}
			selected := inventory.Saved[0]
			if selected.UUID != "a" {
				t.Fatal("selected fixture identity is not first")
			}
			after := selected
			after.ParentPath = ""
			patches := []smartlistPatch{{before: selected, after: &after}}
			// The same incomplete historical image must never qualify as repaired
			// current-live state merely because its operation-owned row is healthy.
			if _, err := stageSmartlistImage(ctx, source, filepath.Join(t.TempDir(), "live.db"), hash, patches); err == nil {
				t.Fatal("current-live stage accepted unrelated remaining damage")
			}
			destination := filepath.Join(t.TempDir(), "historical.db")
			amendment := smartlistHistoricalAmendment{entryID: "original-delete", image: databaseState{Path: source, Hash: hash}, patches: patches}
			if _, err := stageHistoricalSmartlistImage(ctx, destination, amendment); err != nil {
				t.Fatal(err)
			}
			staged, err := db.Open(destination, true)
			if err != nil {
				t.Fatal(err)
			}
			defer staged.Close()
			afterColumns, actual, err := readCompleteSmartlistRows(ctx, staged)
			if err != nil {
				t.Fatal(err)
			}
			for i, name := range columns {
				if name == "parentPlaylistPath" {
					original["a"][i] = ""
				}
			}
			if !reflect.DeepEqual(columns, afterColumns) || !reflect.DeepEqual(original, actual) {
				t.Fatalf("unplanned full-row amendment: before=%v after=%v", original, actual)
			}
			updated, err := db.ReadSmartlistInventory(ctx, staged)
			if err != nil || len(updated.Issues) == 0 {
				t.Fatalf("unrelated historical damage was erased: %+v %v", updated, err)
			}
			for _, issue := range updated.Issues {
				if issue.UUID == "a" {
					t.Fatalf("selected row is still damaged: %+v", issue)
				}
			}
			if actual, err := fingerprint(ctx, source); err != nil || actual != hash {
				t.Fatalf("original historical source changed: %q %v", actual, err)
			}
		})
	}
}

func TestSmartlistHistoricalStageRejectsNewCauseUnderSameIssue(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := database.Exec(`DELETE FROM Smartlist;
		INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules)
		VALUES ('t','Selected removal','House;','','','opaque-t'),
		('u','Unchanged invalid dual successor','House;','House;','t','opaque-u')`); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(lib, "Database2", "m.db")
	inventory, hash, err := readSmartlistVersion(ctx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Issues) != 1 || inventory.Issues[0].UUID != "u" || inventory.Issues[0].Kind != "sibling_link_invalid" {
		t.Fatalf("wrong original public issue: %+v", inventory.Issues)
	}
	amendment := smartlistHistoricalAmendment{entryID: "original-delete", image: databaseState{Path: source, Hash: hash}, patches: []smartlistPatch{{before: inventory.Saved[0]}}}
	destination := filepath.Join(t.TempDir(), "invalid.db")
	if result, err := stageHistoricalSmartlistImage(ctx, destination, amendment); err == nil || result != "" {
		t.Fatalf("same public issue concealed a new missing-target cause: %q %v", result, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("rejected historical image survived: %v", err)
	}
	if actual, err := fingerprint(ctx, source); err != nil || actual != hash {
		t.Fatalf("failed historical stage changed original: %q %v", actual, err)
	}
}
