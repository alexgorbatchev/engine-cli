package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistRollbackRejectsNonFileAndMissingTargetsBeforeRestore(t *testing.T) {
	for _, kind := range []string{"missing entry", "directory entry", "missing parent", "file parent", "removed directory", "removed symlink", "database address", "history address"} {
		t.Run(kind, func(t *testing.T) {
			lib, dir, journal := smartlistTargetFixture(t)
			ctx := context.Background()
			if err := writeSmartlistTarget(ctx, lib, dir, journal.Images[0], false); err != nil {
				t.Fatal(err)
			}
			image := &journal.Images[1]
			path := filepath.Join(lib, image.Target)
			switch kind {
			case "missing entry", "directory entry":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "directory entry" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "missing parent", "file parent":
				image.Database, image.Target = true, filepath.Join("Database2", "m.db")
				if kind == "file parent" {
					if err := os.WriteFile(filepath.Join(lib, "Database2"), []byte("external parent"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "removed directory", "removed symlink":
				image.Original, image.OriginalHash = "", ""
				removed := smartlistRemovedEntry(path)
				if kind == "removed directory" {
					if err := os.Mkdir(removed, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(path, removed); err != nil {
					t.Skipf("native symlink unavailable: %v", err)
				}
			case "database address":
				image.Database, image.Target = true, filepath.Join("Database2", "other.db")
			case "history address":
				image.Target = filepath.Join(".engine-cli", "unrelated", filepath.Base(image.Target))
			}
			if image.Database {
				source, _ := smartlistHistoryLibrary(t)
				for _, name := range []string{image.Original, image.Staged} {
					path := filepath.Join(dir, name)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := copyDatabase(ctx, filepath.Join(source, "Database2", "m.db"), path); err != nil {
						t.Fatal(err)
					}
				}
				hash, err := fingerprint(ctx, filepath.Join(dir, image.Original))
				if err != nil {
					t.Fatal(err)
				}
				image.OriginalHash, image.StagedHash = hash, hash
			}
			if err := restoreSmartlistTargets(ctx, lib, dir, journal); err == nil {
				t.Fatal("unsafe target granted rollback")
			}
			first, err := os.ReadFile(filepath.Join(lib, journal.Images[0].Target))
			if err != nil || string(first) != "corrected first" {
				t.Fatalf("earlier target restored before complete validation: %q %v", first, err)
			}
		})
	}
}

func TestSmartlistReferencesRejectConflictingSnapshots(t *testing.T) {
	lib := t.TempDir()
	path := filepath.Join(lib, ".engine-cli", "history", "image", "m.db")
	state := databaseState{Path: path, Hash: "original"}
	p := &smartlistPrepared{}
	entry := &Entry{Before: &snapshot{Databases: map[string]databaseState{"native": state}}, After: &snapshot{Databases: map[string]databaseState{"native": state}}}
	if err := p.referenceEntry(lib, entry); err != nil {
		t.Fatal(err)
	}
	if len(p.journal.References) != 1 {
		t.Fatal("same snapshot was not deduplicated")
	}
	entry.After.Databases["native"] = databaseState{Path: path, Hash: "external"}
	if err := p.referenceEntry(lib, entry); err == nil {
		t.Fatal("conflicting logical snapshot accepted")
	}
	if len(p.journal.References) != 1 || p.journal.References[0].Hash != state.Hash {
		t.Fatal("original reference replaced")
	}
	if err := p.referenceEntry(lib, &Entry{}); err != nil {
		t.Fatal(err)
	}
}

func TestSmartlistReferencedStorageMustRemainConfined(t *testing.T) {
	for _, kind := range []string{"foreign tree", "missing image", "directory image", "file ancestor", "duplicate reference", "cancelled sync"} {
		t.Run(kind, func(t *testing.T) {
			lib, dir, journal := smartlistTargetFixture(t)
			ctx := context.Background()
			if err := writeSmartlistTarget(ctx, lib, dir, journal.Images[0], false); err != nil {
				t.Fatal(err)
			}
			reference := smartlistJournalReference{Path: filepath.Join(".engine-cli", "history", "snapshots", "m.db"), Hash: "observed"}
			switch kind {
			case "foreign tree":
				reference.Path = filepath.Join(".engine-cli", "other", "snapshots", "m.db")
			case "directory image":
				if err := os.MkdirAll(filepath.Join(lib, reference.Path), 0700); err != nil {
					t.Fatal(err)
				}
			case "file ancestor":
				if err := os.WriteFile(filepath.Join(lib, ".engine-cli", "history", "snapshots"), []byte("external"), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate reference":
				source, _ := smartlistHistoryLibrary(t)
				parent := filepath.Dir(filepath.Join(lib, reference.Path))
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				if err := copyDatabase(ctx, filepath.Join(source, "Database2", "m.db"), filepath.Join(lib, reference.Path)); err != nil {
					t.Fatal(err)
				}
				hash, err := fingerprint(ctx, filepath.Join(lib, reference.Path))
				if err != nil {
					t.Fatal(err)
				}
				reference.Hash = hash
			case "cancelled sync":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if err := syncSmartlistReferences(cancelled, dir, []smartlistJournalReference{reference}); err == nil {
					t.Fatal("cancelled sync accepted")
				}
				return
			}
			journal.References = []smartlistJournalReference{reference}
			if kind == "duplicate reference" {
				journal.References = append(journal.References, reference)
			}
			if err := restoreSmartlistTargets(ctx, lib, dir, journal); err == nil {
				t.Fatal("unsafe reference granted rollback")
			}
			first, err := os.ReadFile(filepath.Join(lib, journal.Images[0].Target))
			if err != nil || string(first) != "corrected first" {
				t.Fatalf("partial restore: %q %v", first, err)
			}
		})
	}
}
