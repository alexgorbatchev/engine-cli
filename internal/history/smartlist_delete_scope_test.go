package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistSemicolonSiblingDeleteUndo(t *testing.T) {
	ctx := t.Context()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := db.CreatePlaylist(ctx, database, "House;Live", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime)
 VALUES('sibling','Sibling','House;Live;','','','opaque-sibling','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Database2", "m.db")
	before, err := fingerprint(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := Run(ctx, lib, "playlist.rm", ScopeCollection, nil, func(ctx context.Context) error {
		return db.DeletePlaylist(ctx, database, 1, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	var rules string
	if err := database.QueryRow(`SELECT rules FROM Smartlist WHERE listUuid='sibling'`).Scan(&rules); err != nil || rules != "opaque-sibling" {
		t.Fatalf("sibling changed: rules=%q error=%v", rules, err)
	}
	if entry == nil {
		t.Fatal("missing delete history")
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	after, err := fingerprint(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("Undo did not restore all native database rows")
	}
}
