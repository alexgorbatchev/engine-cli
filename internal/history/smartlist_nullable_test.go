package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistNullableHealthyRepairDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	lib, d := smartlistHistoryLibrary(t)
	if _, err := d.Exec(`DELETE FROM Smartlist; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('nullable','Nullable',NULL,NULL,NULL,'opaque')`); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(lib, "Database2", "m.db")
	before, err := fingerprint(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || review == nil || review.Removed != 0 || review.Reconnected != 0 {
		t.Fatalf("healthy nullable preview: %+v %v", review, err)
	}
	if _, err := ApplySmartlistRepair(ctx, lib, review.PlanToken); err != nil {
		t.Fatal(err)
	}
	if after, err := fingerprint(ctx, source); err != nil || after != before {
		t.Fatalf("healthy nullable repair changed native data: %s %v", after, err)
	}
	if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("healthy nullable repair enrolled storage: %v", err)
	}
}

func TestSmartlistStageRetainsUnchangedNativeNullLinks(t *testing.T) {
	ctx := context.Background()
	lib, d := smartlistHistoryLibrary(t)
	if _, err := d.Exec(`DELETE FROM Smartlist; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('nullable','Nullable','Missing;',NULL,NULL,'opaque'),('untouched','Untouched',NULL,'House;',NULL,'untouched rules')`); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(lib, "Database2", "m.db")
	before, hash, err := readSmartlistVersion(ctx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	row := before.Saved[0]
	after := row
	after.ParentPath = "House;"
	destination := filepath.Join(t.TempDir(), "nullable.db")
	if _, err := stageSmartlistImage(ctx, source, destination, hash, []smartlistPatch{{before: row, after: &after}}); err != nil {
		t.Fatal(err)
	}
	staged, err := db.Open(destination, true)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	var preserved int
	if err := staged.QueryRow(`SELECT count(*) FROM Smartlist WHERE (listUuid='nullable' AND parentPlaylistPath='House;' AND nextPlaylistPath IS NULL AND nextListUuid IS NULL AND rules='opaque') OR (listUuid='untouched' AND parentPlaylistPath IS NULL AND nextPlaylistPath='House;' AND nextListUuid IS NULL AND rules='untouched rules')`).Scan(&preserved); err != nil || preserved != 2 {
		t.Fatalf("nullable native links changed outside patch: %d %v", preserved, err)
	}
	if after, err := fingerprint(ctx, source); err != nil || after != hash {
		t.Fatalf("original nullable rows changed: %s %v", after, err)
	}
}
