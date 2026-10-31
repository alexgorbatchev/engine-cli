package history

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistParentClassifiesMixedNativeHistory(t *testing.T) {
	ctx := context.Background()
	lib, d := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, d, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	removed := recordSmartlistMutation(t, lib, d, "playlist.rm", `DELETE FROM Playlist WHERE id=2`)
	evidence, err := readSmartlistEvidence(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, parent, entry string
		remove              bool
	}{
		{"House;", "Renamed;", "", false},
		{"Sets;", "", removed.ID, true},
		{"Unknown;", "", "", true},
		{"", "", "", false},
	} {
		row := db.SmartlistRecord{UUID: "saved", Name: "Saved", ParentPath: tc.path}
		decision, err := classifySmartlistParent(ctx, evidence, row)
		if err != nil || decision == nil || decision.row != row || decision.parent != tc.parent || decision.remove != tc.remove || decision.deleteEntry != tc.entry {
			t.Fatalf("path %q: decision=%+v error=%v", tc.path, decision, err)
		}
	}
	if current, err := readSmartlistEvidence(ctx, lib); err != nil || current.hash != evidence.hash {
		t.Fatalf("classification mutated library: %+v %v", current, err)
	}
}

func TestSmartlistParentUnusableHistoryMeansRemovalWithoutUndo(t *testing.T) {
	lib, d := smartlistHistoryLibrary(t)
	for _, sql := range []string{
		`UPDATE Playlist SET title='Renamed' WHERE id=1`,
		`INSERT INTO Playlist(title,parentListId,nextListId) VALUES ('House',0,0)`,
		`UPDATE Playlist SET title='Other' WHERE id=3`,
	} {
		recordSmartlistMutation(t, lib, d, "playlist.rename", sql)
	}
	evidence, err := readSmartlistEvidence(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := classifySmartlistParent(context.Background(), evidence, evidence.current.Saved[0])
	if err != nil || decision == nil || !decision.remove || decision.deleteEntry != "" {
		t.Fatalf("ambiguous parent left stranded or invented Undo: %+v %v", decision, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if decision, err := classifySmartlistParent(ctx, evidence, evidence.current.Saved[0]); err == nil || decision != nil {
		t.Fatalf("cancellation became destructive classification: %+v %v", decision, err)
	}
}
