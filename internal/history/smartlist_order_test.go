package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistNativeOrderEvidenceChangedSnapshot(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('preceding','Preceding','','House;','','keep')`); err != nil {
		t.Fatal(err)
	}
	entry := recordSmartlistMutation(t, lib, database, "playlist.move", `UPDATE Playlist SET parentListId=2,nextListId=0 WHERE id=1; UPDATE Playlist SET nextListId=0 WHERE id=2`)
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
	proven := false
	for _, patch := range plan.patches {
		if patch.before.UUID == "preceding" && patch.after != nil && patch.after.NextPath == "Sets;" && patch.after.NextUUID == "" {
			proven = true
		}
	}
	if !proven {
		t.Fatalf("native old successor lost: %+v", plan.patches)
	}
	state := entry.Before.Databases[filepath.Join(lib, "Database2", "m.db")]
	d, err := db.Open(state.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, mutationErr := d.Exec(`UPDATE Playlist SET nextListId=999 WHERE id=1`)
	closeErr := d.Close()
	if mutationErr != nil || closeErr != nil {
		t.Fatalf("mutation=%v close=%v", mutationErr, closeErr)
	}
	if orders, err := readSmartlistOrderEvidence(ctx, lib, evidence); err == nil || orders != nil {
		t.Fatalf("changed original snapshot accepted: %+v %v", orders, err)
	}
}
