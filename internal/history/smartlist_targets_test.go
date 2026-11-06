package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistTargetsRejectAllUnknownStatesBeforeRestore(t *testing.T) {
	for _, damage := range []string{"external target", "damaged original", "malformed target", "symlink parent"} {
		t.Run(damage, func(t *testing.T) {
			lib, dir, journal := smartlistTargetFixture(t)
			ctx := context.Background()
			first := filepath.Join(lib, journal.Images[0].Target)
			second := filepath.Join(lib, journal.Images[1].Target)
			if err := writeSmartlistTarget(ctx, lib, dir, journal.Images[0], false); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "external target":
				if err := os.WriteFile(second, []byte("external"), 0600); err != nil {
					t.Fatal(err)
				}
			case "damaged original":
				if err := os.WriteFile(filepath.Join(dir, journal.Images[1].Original), []byte("external"), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed target":
				journal.Images[1].Target = ".engine-cli/history/not-an-entry.json"
			case "symlink parent":
				parent := filepath.Dir(second)
				retained := parent + "-retained"
				if err := os.Rename(parent, retained); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(retained, parent); err != nil {
					t.Skipf("native symlink unavailable: %v", err)
				}
			}
			if err := restoreSmartlistTargets(ctx, lib, dir, journal); err == nil {
				t.Fatal("unknown state granted rollback publication")
			}
			data, err := os.ReadFile(first)
			if err != nil || string(data) != "corrected first" {
				t.Fatalf("first target changed before full validation: %q %v", data, err)
			}
		})
	}
}

func TestSmartlistTargetsRollbackPartialPublicationAndNewEntry(t *testing.T) {
	ctx := context.Background()
	lib, dir, journal := smartlistTargetFixture(t)
	newEntry := journal.Images[1]
	newEntry.Original, newEntry.OriginalHash = "", ""
	journal.Images[1] = newEntry
	if err := os.Remove(filepath.Join(lib, newEntry.Target)); err != nil {
		t.Fatal(err)
	}
	if err := validateSmartlistTargets(ctx, lib, dir, journal, false); err != nil {
		t.Fatal(err)
	}
	if err := prepareSmartlistJournal(ctx, dir, journal); err != nil {
		t.Fatal(err)
	}
	for _, image := range journal.Images {
		if err := writeSmartlistTarget(ctx, lib, dir, image, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreSmartlistTargets(ctx, lib, dir, journal); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(lib, journal.Images[0].Target))
	if err != nil || string(first) != "original first" {
		t.Fatalf("original not restored: %q %v", first, err)
	}
	if _, err := os.Lstat(filepath.Join(lib, newEntry.Target)); !os.IsNotExist(err) {
		t.Fatalf("new entry remains: %v", err)
	}
	if _, err := readSmartlistJournal(ctx, dir); err != nil {
		t.Fatalf("rollback discarded intent/evidence: %v", err)
	}
	if err := restoreSmartlistTargets(ctx, lib, dir, journal); err != nil {
		t.Fatalf("rollback retry not idempotent: %v", err)
	}
}

func TestSmartlistTargetsRejectChangedRemovedEvidenceBeforeAnyRestore(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "already removed", true: "still published"}[present], func(t *testing.T) {
			ctx := context.Background()
			lib, dir, journal := smartlistTargetFixture(t)
			image := &journal.Images[1]
			image.Original, image.OriginalHash = "", ""
			path := filepath.Join(lib, image.Target)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			for _, target := range journal.Images {
				if err := writeSmartlistTarget(ctx, lib, dir, target, false); err != nil {
					t.Fatal(err)
				}
			}
			if !present {
				if err := writeSmartlistTarget(ctx, lib, dir, *image, true); err != nil {
					t.Fatal(err)
				}
			}
			removed := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".repair-removed")
			if err := os.WriteFile(removed, []byte("external evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := restoreSmartlistTargets(ctx, lib, dir, journal); err == nil {
				t.Fatal("changed archived evidence accepted")
			}
			data, err := os.ReadFile(filepath.Join(lib, journal.Images[0].Target))
			if err != nil || string(data) != "corrected first" {
				t.Fatalf("first target restored before all evidence validated: %q %v", data, err)
			}
		})
	}
}

func smartlistTargetFixture(t *testing.T) (string, string, smartlistJournal) {
	t.Helper()
	lib := t.TempDir()
	dir := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(lib, ".engine-cli", "history")
	if err := os.MkdirAll(history, 0700); err != nil {
		t.Fatal(err)
	}
	journal := smartlistJournal{Version: 1, Library: "native-library"}
	for i, name := range []string{"first", "second"} {
		id := "0000000000000000000000000000000" + string(rune('1'+i))
		original, staged := "original "+name, "corrected "+name
		image := smartlistJournalImage{Target: filepath.Join(".engine-cli", "history", id+".json"), Original: name + "-original.json", OriginalHash: hashText(original), Staged: name + "-staged.json", StagedHash: hashText(staged)}
		for path, data := range map[string]string{filepath.Join(dir, image.Original): original, filepath.Join(dir, image.Staged): staged, filepath.Join(lib, image.Target): original} {
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		journal.Images = append(journal.Images, image)
	}
	return lib, dir, journal
}
