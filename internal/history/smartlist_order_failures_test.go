package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistOrderRefusesUnreadableOrAmbiguousNativeImages(t *testing.T) {
	for _, kind := range []string{"missing image", "stale fingerprint", "missing linkage column", "null linkage", "duplicate identity"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			path := filepath.Join(lib, "Database2", "m.db")
			switch kind {
			case "missing linkage column":
				if _, err := database.Exec(`ALTER TABLE Playlist RENAME COLUMN nextListId TO lostNextListId`); err != nil {
					t.Fatal(err)
				}
			case "null linkage":
				if _, err := database.Exec(`UPDATE Playlist SET nextListId=NULL WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			case "duplicate identity":
				// Deliberately damaged schema retains all native row values but
				// loses the primary key. A map must not silently pick a winner.
				if _, err := database.Exec(`ALTER TABLE Playlist RENAME TO RetainedPlaylist;
					CREATE TABLE Playlist AS SELECT * FROM RetainedPlaylist;
					INSERT INTO Playlist SELECT * FROM RetainedPlaylist WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			}
			hash, err := fingerprint(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			expected := hash
			if kind == "stale fingerprint" {
				expected = "different-image"
			}
			if kind == "missing image" {
				path = filepath.Join(lib, "Database2", "absent.db")
			}
			if links, err := readSmartlistOrdinaryLinks(ctx, path, expected); err == nil || links != nil {
				t.Fatalf("invalid native order was accepted: %v %v", links, err)
			}
			if kind == "missing image" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("read enrolled an absent native image: %v", err)
				}
			} else if actual, err := fingerprint(ctx, path); err != nil || actual != hash {
				t.Fatalf("refused image was changed: %s %v", actual, err)
			}
		})
	}
}
