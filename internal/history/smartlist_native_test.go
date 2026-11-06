package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// The fixture retains the recovered native catalog's 45 mask-1 statements,
// including native views and triggers. Its adjacent provenance binds every SQL
// input to committed engine-dj-app source; it contains no user library data.
func TestSmartlistStageRecoveredNativeSchema(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "native.db")
	d, err := db.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	schema, err := os.ReadFile("testdata/smartlist-native-3.0.2.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO Information(id,uuid,schemaVersionMajor,schemaVersionMinor,schemaVersionPatch) VALUES(1,'synthetic-native-schema',3,0,2);
		INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Renamed',0,0,1);
		INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Child',1,0,1);
		INSERT INTO Track(title,path) VALUES('Unchanged native track','synthetic.wav');
		INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,1);
		INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime)
		VALUES('saved','Saved','Old;','','','{"opaque":[1,null,"λ"]}','2026-10-29');
	`); err != nil {
		t.Fatal(err)
	}
	before, hash, err := readSmartlistVersion(ctx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	after := before.Saved[0]
	after.ParentPath = "Renamed;"
	destination := filepath.Join(t.TempDir(), "repaired.db")
	if _, err := stageSmartlistImage(ctx, source, destination, hash, []smartlistPatch{{before: before.Saved[0], after: &after}}); err != nil {
		t.Fatal(err)
	}
	staged, err := db.Open(destination, true)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	var parent, rules, timestamp, title string
	if err := staged.QueryRow(`SELECT parentPlaylistPath,rules,CAST(lastEditTime AS TEXT) FROM Smartlist WHERE listUuid='saved'`).Scan(&parent, &rules, &timestamp); err != nil {
		t.Fatal(err)
	}
	if parent != after.ParentPath || rules != `{"opaque":[1,null,"λ"]}` || timestamp != "2026-10-29" {
		t.Fatalf("native row damaged: %q %q %q", parent, rules, timestamp)
	}
	if err := staged.QueryRow(`SELECT title FROM Track WHERE id=1`).Scan(&title); err != nil || title != "Unchanged native track" {
		t.Fatalf("native track damaged: %q %v", title, err)
	}
	var prepared int
	if err := staged.QueryRow(`SELECT trackId FROM PreparelistEntity WHERE id=1`).Scan(&prepared); err != nil || prepared != 1 {
		t.Fatalf("native preparation damaged: %d %v", prepared, err)
	}
	if current, err := fingerprint(ctx, source); err != nil || current != hash {
		t.Fatalf("native source mutated: %q %v", current, err)
	}
}
