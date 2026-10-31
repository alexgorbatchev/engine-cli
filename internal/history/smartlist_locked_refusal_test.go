package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistLockedPlannerRefusesInvalidCurrentEvidence(t *testing.T) {
	for _, kind := range []string{"healthy", "stale consent", "damaged schema", "pending", "blocked archive"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			if kind != "healthy" {
				if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;'`); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := lock(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			}()
			token := ""
			switch kind {
			case "stale consent":
				token = "obsolete consent"
			case "damaged schema":
				if _, err := database.Exec(`ALTER TABLE Smartlist RENAME COLUMN rules TO unrelatedRules; ALTER TABLE Smartlist RENAME COLUMN listUuid TO unrelatedIdentity`); err != nil {
					t.Fatal(err)
				}
			case "pending":
				active := filepath.Join(filepath.Dir(dir), "smartlist-repair")
				if err := os.Mkdir(active, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(active, smartlistJournalName), []byte("retained pending evidence"), 0600); err != nil {
					t.Fatal(err)
				}
			case "blocked archive":
				if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "smartlist-repairs"), []byte("retained archive"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(lib, "Database2", "m.db")
			before, err := fingerprint(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			result, err := applySmartlistRepairLocked(ctx, lib, dir, token)
			if kind == "healthy" {
				if err != nil || result == nil || result.Applied {
					t.Fatalf("healthy locked repair: %+v %v", result, err)
				}
			} else if err == nil || result != nil {
				t.Fatalf("invalid locked repair accepted: %+v %v", result, err)
			}
			after, err := fingerprint(ctx, path)
			if err != nil || before != after {
				t.Fatalf("refusal changed native database: %v", err)
			}
			listed, err := entries(dir)
			if err != nil || len(listed) != 0 {
				t.Fatalf("refusal created Undo: %+v %v", listed, err)
			}
		})
	}
}

func TestSmartlistPublicRepairRejectsUnavailableLibraries(t *testing.T) {
	for _, kind := range []string{"empty", "missing", "regular file", "missing database"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			lib := root
			switch kind {
			case "empty":
				lib = ""
			case "missing":
				lib = filepath.Join(root, "missing")
			case "regular file":
				lib = filepath.Join(root, "file")
				if err := os.WriteFile(lib, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := PreviewSmartlistRepair(context.Background(), lib); result != nil || err == nil {
				t.Fatalf("preview unavailable library: %+v %v", result, err)
			}
			if result, err := ApplySmartlistRepair(context.Background(), lib, "reviewed"); result != nil || err == nil {
				t.Fatalf("apply unavailable library: %+v %v", result, err)
			}
			if kind != "missing database" {
				if result, err := RecoverSmartlistRepair(context.Background(), lib); result != nil || err == nil {
					t.Fatalf("recover unavailable library: %+v %v", result, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("invalid request enrolled storage: %v", err)
			}
		})
	}
}
