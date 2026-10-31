package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSmartlistReviewTokenRejectsUnreadableHistory(t *testing.T) {
	for _, kind := range []string{"directory", "entry"} {
		t.Run(kind, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, entry.ID+".json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			restricted := path
			if kind == "directory" {
				restricted = dir
			}
			smartlistRestrict(t, restricted, 0)
			if token, err := smartlistPlanToken(context.Background(), lib, "observed", SmartlistRepairReview{}); token != "" || !os.IsPermission(err) {
				t.Fatalf("unreadable history granted consent token: %q %v", token, err)
			}
			if err := os.Chmod(restricted, 0700); err != nil {
				t.Fatal(err)
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
				t.Fatalf("token refusal changed history: %q %v", after, err)
			}
		})
	}
}

func TestSmartlistRollbackPermissionFailureRetainsPendingEvidence(t *testing.T) {
	for _, kind := range []string{"unreadable target", "unwritable target directory", "unreadable original"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib, dir, journal := smartlistTargetFixture(t)
			if err := prepareSmartlistJournal(ctx, dir, journal); err != nil {
				t.Fatal(err)
			}
			first := filepath.Join(lib, journal.Images[0].Target)
			if err := writeSmartlistTarget(ctx, lib, dir, journal.Images[0], false); err != nil {
				t.Fatal(err)
			}
			var restricted string
			switch kind {
			case "unreadable target":
				restricted = filepath.Join(lib, journal.Images[1].Target)
				smartlistRestrict(t, restricted, 0)
			case "unwritable target directory":
				restricted = filepath.Dir(first)
				smartlistRestrict(t, restricted, 0500)
			case "unreadable original":
				restricted = filepath.Join(dir, journal.Images[1].Original)
				smartlistRestrict(t, restricted, 0)
			}
			if err := restoreSmartlistTargets(ctx, lib, dir, journal); err == nil {
				t.Fatal("rollback silently accepted inaccessible evidence or publication")
			}
			if data, err := os.ReadFile(first); err != nil || string(data) != "corrected first" {
				t.Fatalf("failed rollback overwrote first target: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(dir, smartlistJournalName)); err != nil {
				t.Fatalf("failed rollback discarded pending evidence: %v", err)
			}
			if err := os.Chmod(restricted, 0700); err != nil {
				t.Fatal(err)
			}
			if err := restoreSmartlistTargets(ctx, lib, dir, journal); err != nil {
				t.Fatalf("rollback could not retry after access restoration: %v", err)
			}
		})
	}
}

func TestSmartlistPublicationRejectsUnreadableStage(t *testing.T) {
	lib, dir, journal := smartlistTargetFixture(t)
	image := journal.Images[0]
	stage := filepath.Join(dir, image.Staged)
	smartlistRestrict(t, stage, 0)
	if err := writeSmartlistTarget(context.Background(), lib, dir, image, false); !os.IsPermission(err) {
		t.Fatalf("unreadable stage published: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(lib, image.Target)); err != nil || string(data) != "original first" {
		t.Fatalf("failed publication changed target: %q %v", data, err)
	}
}

func TestSmartlistPreparationCannotEnrollReadOnlyArchive(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;'`); err != nil {
		t.Fatal(err)
	}
	plan, err := planSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(filepath.Dir(dir), "smartlist-repairs")
	if err := os.MkdirAll(archive, 0700); err != nil {
		t.Fatal(err)
	}
	smartlistRestrict(t, archive, 0500)
	if prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan); prepared != nil || !os.IsPermission(err) {
		t.Fatalf("read-only archive enrolled repair: %+v %v", prepared, err)
	}
	if entries, err := os.ReadDir(archive); err != nil || len(entries) != 0 {
		t.Fatalf("failed preparation left archive images: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "smartlist-repair", smartlistJournalName)); !os.IsNotExist(err) {
		t.Fatalf("failed preparation published pending intent: %v", err)
	}
	if hash, err := fingerprint(ctx, filepath.Join(lib, "Database2", "m.db")); err != nil || hash != plan.evidence.hash {
		t.Fatalf("preparation changed live database: %q %v", hash, err)
	}
}
