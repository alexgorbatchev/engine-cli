package history

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSmartlistRecoveryRollsBackKilledPublisher(t *testing.T) {
	for _, publications := range []int{1, 2} {
		t.Run(strconv.Itoa(publications), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			lib, database := smartlistHistoryLibrary(t)
			recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			target := filepath.Join(lib, "Database2", "m.db")
			original, err := fingerprint(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			before, err := List(lib)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSmartlistPublicationChild$")
			cmd.Env = append(os.Environ(), "ENGINE_SMARTLIST_PUBLICATION_CHILD="+lib, "ENGINE_SMARTLIST_PUBLICATION_COUNT="+strconv.Itoa(publications))
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
					_ = cmd.Process.Kill() // best effort after a failed assertion
					_ = cmd.Wait()         // reap even when the assertion exits early
				}
			}()
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() || scanner.Text() != "targets published" {
				t.Fatalf("child did not publish: %q %v", scanner.Text(), scanner.Err())
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			if err == nil {
				t.Fatal("killed publisher succeeded")
			}
			active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
			journal, err := readSmartlistJournal(ctx, active)
			if err != nil {
				t.Fatal(err)
			}
			if len(journal.Images) != 2 {
				t.Fatalf("unexpected target count: %d", len(journal.Images))
			}
			for _, image := range journal.Images[:publications] {
				got, err := smartlistJournalHash(ctx, filepath.Join(lib, image.Target), image.Database)
				if err != nil || got != image.StagedHash {
					t.Fatalf("child did not really publish target: %q %v", got, err)
				}
			}
			if _, err := List(lib); err == nil {
				t.Fatal("pending publication exposed history")
			}
			recovery, err := RecoverSmartlistRepair(ctx, lib)
			if err != nil || recovery == nil || !recovery.RolledBack || recovery.RestoredTargets != 2 {
				t.Fatalf("recovery: %+v %v", recovery, err)
			}
			if got, err := fingerprint(ctx, target); err != nil || got != original {
				t.Fatalf("live original not restored: %q %v", got, err)
			}
			after, err := List(lib)
			if err != nil || len(after) != len(before) || after[0].ID != before[0].ID {
				t.Fatalf("history not restored: %+v %v", after, err)
			}
			if _, err := os.Stat(filepath.Join(active, "rolled-back.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(active, journal.Images[1].Original)); err != nil {
				t.Fatalf("rollback evidence discarded: %v", err)
			}
		})
	}
}

func TestSmartlistPublicationChild(t *testing.T) {
	lib := os.Getenv("ENGINE_SMARTLIST_PUBLICATION_CHILD")
	if lib == "" {
		return
	}
	count, err := strconv.Atoi(os.Getenv("ENGINE_SMARTLIST_PUBLICATION_COUNT"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	plan, err := planSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSmartlistTargets(ctx, lib, prepared.dir, prepared.journal, false); err != nil {
		t.Fatal(err)
	}
	if err := prepareSmartlistJournal(ctx, prepared.dir, prepared.journal); err != nil {
		t.Fatal(err)
	}
	for _, image := range prepared.journal.Images[:count] {
		if err := writeSmartlistTarget(ctx, lib, prepared.dir, image, false); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("targets published")
	for {
		time.Sleep(time.Hour)
	}
}
