package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistStageNativeFailuresPreserveSourceAndRemoveCandidate(t *testing.T) {
	for _, kind := range []string{"missing fingerprint", "missing source", "missing row", "renamed row", "native trigger refusal", "missing stage parent", "malformed saved schema"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if _, err := database.Exec(`UPDATE Playlist SET title='Renamed' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if kind == "native trigger refusal" {
				if _, err := database.Exec(`CREATE TRIGGER refuse_patch BEFORE UPDATE ON Smartlist BEGIN SELECT RAISE(ABORT,'native constraint refusal'); END`); err != nil {
					t.Fatal(err)
				}
			}
			source := filepath.Join(lib, "Database2", "m.db")
			inventory, hash, err := readSmartlistVersion(ctx, source, "")
			if err != nil {
				t.Fatal(err)
			}
			before := inventory.Saved[0]
			after := before
			after.ParentPath = "Renamed;"
			expected := hash
			destination := filepath.Join(t.TempDir(), "stage.db")
			switch kind {
			case "missing fingerprint":
				expected = ""
			case "missing source":
				source += ".absent"
			case "missing row":
				before.UUID = "absent"
			case "renamed row":
				after.Name = "unauthorized title"
			case "missing stage parent":
				destination = filepath.Join(t.TempDir(), "absent", "stage.db")
			case "malformed saved schema":
				if _, err := database.Exec(`ALTER TABLE Smartlist RENAME COLUMN nextListUuid TO invalidLink`); err != nil {
					t.Fatal(err)
				}
				expected, err = fingerprint(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
			}
			if result, err := stageSmartlistImage(ctx, source, destination, expected, []smartlistPatch{{before: before, after: &after}}); err == nil || result != "" {
				t.Fatalf("unsafe stage accepted: %q %v", result, err)
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("failed stage retained candidate: %v", err)
			}
			actual, err := fingerprint(ctx, filepath.Join(lib, "Database2", "m.db"))
			wanted := hash
			if kind == "malformed saved schema" {
				wanted = expected
			}
			if err != nil || actual != wanted {
				t.Fatalf("failed stage changed native source: %v", err)
			}
		})
	}
}

func TestSmartlistPublicationRefusesDisappearingStage(t *testing.T) {
	for _, kind := range []string{"missing", "directory"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, dir, journal := smartlistTargetFixture(t)
			if err := validateSmartlistTargets(ctx, lib, dir, journal, false); err != nil {
				t.Fatal(err)
			}
			image := journal.Images[0]
			path := filepath.Join(dir, image.Staged)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeSmartlistTarget(ctx, lib, dir, image, false); err == nil {
				t.Fatal("lost stage granted publication")
			}
			data, err := os.ReadFile(filepath.Join(lib, image.Target))
			if err != nil || string(data) != "original first" {
				t.Fatalf("lost stage changed live target: %q %v", data, err)
			}
		})
	}
}
