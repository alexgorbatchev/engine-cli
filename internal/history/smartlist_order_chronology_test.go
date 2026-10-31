package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistChronologicalUnlinkPreservesConvergingChains(t *testing.T) {
	for _, tc := range []struct {
		name, firstOperation, first, second string
	}{
		{"successive deletes", "playlist.rm", `DELETE FROM Playlist WHERE id=1`, `DELETE FROM Playlist WHERE id=2`},
		{"reverse deletes", "playlist.rm", `DELETE FROM Playlist WHERE id=2; UPDATE Playlist SET nextListId=3 WHERE id=1`, `DELETE FROM Playlist WHERE id=1`},
		{"move then delete", "playlist.move", `UPDATE Playlist SET parentListId=3,nextListId=0 WHERE id=1`, `DELETE FROM Playlist WHERE id=2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testSmartlistChronologicalUnlink(t, tc.firstOperation, tc.first, tc.second)
		})
	}
}

func testSmartlistChronologicalUnlink(t *testing.T, operation, first, second string) {
	t.Helper()
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := db.CreatePlaylist(ctx, database, "Third", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES
	('sA','Before A','','House;','','rule-a'),('sB','Before B','','Sets;','','rule-b')`); err != nil {
		t.Fatal(err)
	}
	recordSmartlistMutation(t, lib, database, operation, first)
	recordSmartlistMutation(t, lib, database, "playlist.rm", second)
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
	actual := make(map[string]db.SmartlistRecord)
	for _, patch := range plan.patches {
		if patch.after != nil {
			actual[patch.before.UUID] = *patch.after
		}
	}
	if actual["sA"].NextUUID != "sB" || actual["sA"].NextPath != "" || actual["sB"].NextPath != "Third;" || actual["sB"].NextUUID != "" {
		t.Fatalf("proven chronological native order lost: patches=%+v surviving=%+v", plan.patches, actual)
	}
	source := filepath.Join(lib, "Database2", "m.db")
	destination := filepath.Join(t.TempDir(), "chronological.db")
	if _, err := stageSmartlistImage(ctx, source, destination, evidence.hash, plan.patches); err != nil {
		t.Fatal(err)
	}
	staged, _, err := readSmartlistVersion(ctx, destination, "")
	if err != nil || len(staged.Issues) != 0 {
		t.Fatalf("staged=%+v error=%v", staged, err)
	}
	if current, err := fingerprint(ctx, source); err != nil || current != evidence.hash {
		t.Fatalf("live source changed: %q %v", current, err)
	}
}
