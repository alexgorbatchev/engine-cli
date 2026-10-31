package history

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func smartlistJournalFixture(t *testing.T) (string, smartlistJournal) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"original.json": "original entry\n", "staged.json": "corrected entry\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, smartlistJournal{Version: 1, Library: "native-library", Images: []smartlistJournalImage{{Target: ".engine-cli/history/entry.json", Original: "original.json", OriginalHash: hashText("original entry\n"), Staged: "staged.json", StagedHash: hashText("corrected entry\n")}}}
}

func TestSmartlistJournalRejectsInvalidEvidenceBeforeIntent(t *testing.T) {
	for _, kind := range []string{"changed original", "changed stage", "missing original hash", "missing stage", "target escape", "duplicate target", "nested evidence", "symlink evidence", "hardlinked evidence", "database without original", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			dir, journal := smartlistJournalFixture(t)
			ctx := context.Background()
			image := &journal.Images[0]
			switch kind {
			case "changed original":
				image.OriginalHash = hashText("different original")
			case "changed stage":
				image.StagedHash = hashText("different stage")
			case "missing original hash":
				image.OriginalHash = ""
			case "missing stage":
				image.Staged = "absent.json"
			case "target escape":
				image.Target = "../another-library/entry.json"
			case "duplicate target":
				journal.Images = append(journal.Images, *image)
			case "nested evidence":
				image.Staged = "nested/staged.json"
			case "symlink evidence", "hardlinked evidence":
				original := filepath.Join(dir, image.Original)
				alias := filepath.Join(dir, "alias.json")
				link := os.Link
				if kind == "symlink evidence" {
					link = os.Symlink
				}
				if err := link(original, alias); err != nil {
					t.Skipf("native link unavailable: %v", err)
				}
				image.Staged, image.StagedHash = "alias.json", image.OriginalHash
			case "database without original":
				image.Database = true
				image.Original, image.OriginalHash = "", ""
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := prepareSmartlistJournal(ctx, dir, journal); err == nil {
				t.Fatal("invalid rollback evidence published pending intent")
			}
			if _, err := os.Stat(filepath.Join(dir, smartlistJournalName)); !os.IsNotExist(err) {
				t.Fatalf("rejected preparation left pending intent: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "original.json"))
			if err != nil || string(data) != "original entry\n" {
				t.Fatalf("original evidence changed: %q %v", data, err)
			}
		})
	}
}

func TestSmartlistJournalRetainsPendingAndRejectsChangedEvidence(t *testing.T) {
	dir, journal := smartlistJournalFixture(t)
	ctx := context.Background()
	if err := prepareSmartlistJournal(ctx, dir, journal); err != nil {
		t.Fatal(err)
	}
	got, err := readSmartlistJournal(ctx, dir)
	if err != nil || !reflect.DeepEqual(got, &journal) {
		t.Fatalf("durable intent differs: %#v %v", got, err)
	}
	path := filepath.Join(dir, smartlistJournalName)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareSmartlistJournal(ctx, dir, journal); err == nil {
		t.Fatal("overwrote existing pending intent")
	}
	if err := os.WriteFile(filepath.Join(dir, journal.Images[0].Original), []byte("unknown external replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSmartlistJournal(ctx, dir); err == nil {
		t.Fatal("accepted damaged rollback original")
	}
	retained, err := os.ReadFile(path)
	if err != nil || string(retained) != string(original) {
		t.Fatalf("failed validation discarded pending evidence: %q %v", retained, err)
	}
}

func TestSmartlistJournalBindsNativeDatabaseImages(t *testing.T) {
	ctx := context.Background()
	lib, d := smartlistHistoryLibrary(t)
	dir, journal := smartlistJournalFixture(t)
	source := filepath.Join(lib, "Database2", "m.db")
	if _, err := d.Exec(`UPDATE Playlist SET title='Renamed' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	inventory, originalHash, err := readSmartlistVersion(ctx, source, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := copyDatabase(ctx, source, filepath.Join(dir, "original.db")); err != nil {
		t.Fatal(err)
	}
	after := inventory.Saved[0]
	after.ParentPath = "Renamed;"
	stagedHash, err := stageSmartlistImage(ctx, source, filepath.Join(dir, "staged.db"), originalHash, []smartlistPatch{{before: inventory.Saved[0], after: &after}})
	if err != nil {
		t.Fatal(err)
	}
	journal.Images = append(journal.Images, smartlistJournalImage{Target: "Database2/m.db", Database: true, Original: "original.db", OriginalHash: originalHash, Staged: "staged.db", StagedHash: stagedHash})
	if err := prepareSmartlistJournal(ctx, dir, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := readSmartlistJournal(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "original.db"), []byte("damaged rollback database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSmartlistJournal(ctx, dir); err == nil {
		t.Fatal("accepted corrupt native rollback image")
	}
	if got, err := fingerprint(ctx, source); err != nil || got != originalHash {
		t.Fatalf("journal validation changed live source: %q %v", got, err)
	}
}

func TestSmartlistJournalSurvivesKilledOwner(t *testing.T) {
	dir, journal := smartlistJournalFixture(t)
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSmartlistJournalChild$")
	cmd.Env = append(os.Environ(), "ENGINE_SMARTLIST_JOURNAL_CHILD="+dir)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewScanner(output)
	if !reader.Scan() || reader.Text() != "pending durable" {
		t.Fatalf("child did not publish intent: %q %v", reader.Text(), reader.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("killed child unexpectedly succeeded")
	}
	got, err := readSmartlistJournal(context.Background(), dir)
	if err != nil || !reflect.DeepEqual(got, &journal) {
		t.Fatalf("owner death lost pending rollback evidence: %#v %v", got, err)
	}
}

func TestSmartlistJournalChild(t *testing.T) {
	dir := os.Getenv("ENGINE_SMARTLIST_JOURNAL_CHILD")
	if dir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal smartlistJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if err := prepareSmartlistJournal(context.Background(), dir, journal); err != nil {
		t.Fatal(err)
	}
	fmt.Println("pending durable")
	for {
		time.Sleep(time.Hour)
	}
}
