package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistStagePreservesCompleteNativeRows(t *testing.T) {
	ctx := context.Background()
	lib, d := smartlistHistoryLibrary(t)
	if _, err := d.Exec(`ALTER TABLE Smartlist ADD COLUMN futureData BLOB; ALTER TABLE Smartlist ADD COLUMN emptyBlob BLOB; ALTER TABLE Smartlist ADD COLUMN nullableValue TEXT; UPDATE Smartlist SET rules='{"nested":["λ",null,42]}',futureData=x'00ff80',emptyBlob=x'',lastEditTime='2026-10-29 17:23:10'; UPDATE Playlist SET title='Renamed' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(lib, "Database2", "m.db")
	before, hash, err := readSmartlistVersion(ctx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	row := before.Saved[0]
	after := row
	after.ParentPath = "Renamed;"
	destination := filepath.Join(t.TempDir(), "stage.db")
	result, err := stageSmartlistImage(ctx, source, destination, hash, []smartlistPatch{{before: row, after: &after}})
	if err != nil || result == "" || result == hash {
		t.Fatalf("stage: hash=%q err=%v", result, err)
	}
	staged, err := db.Open(destination, true)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	var parent, rules, timestamp, blob string
	if err := staged.QueryRow(`SELECT parentPlaylistPath,rules,CAST(lastEditTime AS TEXT),hex(futureData) FROM Smartlist WHERE listUuid='saved'`).Scan(&parent, &rules, &timestamp, &blob); err != nil {
		t.Fatal(err)
	}
	if parent != "Renamed;" || rules != `{"nested":["λ",null,42]}` || timestamp != "2026-10-29 17:23:10" || blob != "00FF80" {
		t.Fatalf("full native row damaged: %q %q %q %q", parent, rules, timestamp, blob)
	}
	if unchanged, err := fingerprint(ctx, source); err != nil || unchanged != hash {
		t.Fatalf("source mutated: %q %v", unchanged, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("staging enrolled history: %v", err)
	}
}

func TestSmartlistStageRejectsStaleAndInvalidPatches(t *testing.T) {
	for _, kind := range []string{"stale fingerprint", "stale row", "changed identity", "duplicate patch", "unrepaired links", "ignored update", "changed rules", "changed ordinary row", "cancelled", "existing destination", "existing sidecar"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, d := smartlistHistoryLibrary(t)
			if _, err := d.Exec(`UPDATE Playlist SET title='Renamed' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if kind == "ignored update" {
				if _, err := d.Exec(`CREATE TRIGGER ignore_repair BEFORE UPDATE ON Smartlist BEGIN SELECT RAISE(IGNORE); END`); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "changed rules" {
				if _, err := d.Exec(`CREATE TRIGGER damage_rules AFTER UPDATE ON Smartlist BEGIN UPDATE Smartlist SET rules='changed by trigger' WHERE listUuid=NEW.listUuid; END`); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "changed ordinary row" {
				if _, err := d.Exec(`CREATE TRIGGER damage_playlist AFTER UPDATE ON Smartlist BEGIN UPDATE Playlist SET title='Changed' WHERE id=2; END`); err != nil {
					t.Fatal(err)
				}
			}
			source := filepath.Join(lib, "Database2", "m.db")
			before, hash, err := readSmartlistVersion(ctx, source, "")
			if err != nil {
				t.Fatal(err)
			}
			row := before.Saved[0]
			after := row
			after.ParentPath = "Renamed;"
			patches := []smartlistPatch{{before: row, after: &after}}
			expected := hash
			destination := filepath.Join(t.TempDir(), "stage.db")
			switch kind {
			case "stale fingerprint":
				expected = "not-the-source"
			case "stale row":
				patches[0].before.Name = "Other"
			case "changed identity":
				after.UUID = "new-id"
			case "duplicate patch":
				patches = append(patches, patches[0])
			case "unrepaired links":
				after.ParentPath = "Missing;"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "existing destination":
				if err := os.WriteFile(destination, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "existing sidecar":
				if err := os.WriteFile(destination+"-wal", []byte("preserve sidecar"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if hash, err := stageSmartlistImage(ctx, source, destination, expected, patches); err == nil || hash != "" {
				t.Fatalf("accepted invalid stage: %q %v", hash, err)
			}
			if kind == "existing destination" {
				data, err := os.ReadFile(destination)
				if err != nil || string(data) != "preserve" {
					t.Fatalf("existing destination changed: %q %v", data, err)
				}
			} else if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("failed staging left image: %v", err)
			}
			if kind == "existing sidecar" {
				data, err := os.ReadFile(destination + "-wal")
				if err != nil || string(data) != "preserve sidecar" {
					t.Fatalf("existing sidecar changed: %q %v", data, err)
				}
			}
			if actual, err := fingerprint(context.Background(), source); err != nil || actual != hash {
				t.Fatalf("invalid stage changed source: %q %v", actual, err)
			}
		})
	}
}

func TestSmartlistStageDeletionAndHealthyNoEnrollment(t *testing.T) {
	ctx := context.Background()
	lib, _ := smartlistHistoryLibrary(t)
	source := filepath.Join(lib, "Database2", "m.db")
	before, hash, err := readSmartlistVersion(ctx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "absent", "not-created.db")
	if result, err := stageSmartlistImage(ctx, source, destination, hash, nil); err != nil || result != "" {
		t.Fatalf("healthy no-op: %q %v", result, err)
	}
	if _, err := os.Stat(filepath.Dir(destination)); !os.IsNotExist(err) {
		t.Fatalf("healthy plan created storage: %v", err)
	}
	destination = filepath.Join(t.TempDir(), "removed.db")
	if _, err := stageSmartlistImage(ctx, source, destination, hash, []smartlistPatch{{before: before.Saved[0]}}); err != nil {
		t.Fatal(err)
	}
	staged, _, err := readSmartlistVersion(ctx, destination, "")
	if err != nil || len(staged.Saved) != 0 || len(staged.Playlists) != len(before.Playlists) {
		t.Fatalf("deletion affected ordinary rows: %+v %v", staged, err)
	}
}
