package history

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSmartlistReferenceDurabilityFailureCannotAuthorizePublication(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires native Unix permission denial under an unprivileged owner")
	}
	for _, kind := range []string{"read-only referenced image", "read-only image directory"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planSmartlistRepair(ctx, lib)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan)
			if err != nil {
				t.Fatal(err)
			}
			var path string
			for _, reference := range prepared.journal.References {
				if filepath.Dir(filepath.Dir(reference.Path)) == filepath.Join(".engine-cli", "smartlist-repairs") {
					path = filepath.Join(lib, reference.Path)
					break
				}
			}
			if path == "" {
				t.Fatal("repair did not stage a corrected reference")
			}
			mode := os.FileMode(0600)
			if kind == "read-only image directory" {
				path, mode = filepath.Dir(path), 0700
			}
			if err := os.Chmod(path, mode&^0200); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(path, mode); err != nil {
					t.Error(err)
				}
			})
			entryPath := filepath.Join(dir, entry.ID+".json")
			original, err := os.ReadFile(entryPath)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(lib, "Database2", "m.db")
			hash, err := fingerprint(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareSmartlistJournal(ctx, prepared.dir, prepared.journal); err == nil {
				t.Fatal("unsynced reference granted durable publication intent")
			}
			if _, err := os.Lstat(filepath.Join(prepared.dir, smartlistJournalName)); !os.IsNotExist(err) {
				t.Fatalf("reference sync failure published pending: %v", err)
			}
			if data, err := os.ReadFile(entryPath); err != nil || string(data) != string(original) {
				t.Fatalf("reference sync failure changed live history: %v", err)
			}
			if got, err := fingerprint(ctx, target); err != nil || got != hash {
				t.Fatalf("reference sync failure changed live native data: %s %v", got, err)
			}
		})
	}
}
