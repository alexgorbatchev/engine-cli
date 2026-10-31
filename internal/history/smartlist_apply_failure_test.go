package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestSmartlistApplyRollsBackAfterActualPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := open(ctx, filepath.Join(lib, "Database2", "m.db"), sqlite.OpenReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := locked.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := sqlitex.ExecuteTransient(locked, "BEGIN IMMEDIATE", nil); err != nil {
		t.Fatal(err)
	}
	transaction := true
	publication, stop := context.WithCancel(ctx)
	defer stop()
	type outcome struct {
		result *SmartlistRepairResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := ApplySmartlistRepair(publication, lib, review.PlanToken)
		done <- outcome{result, err}
	}()
	joined := false
	defer func() {
		stop()
		if transaction {
			if err := sqlitex.ExecuteTransient(locked, "ROLLBACK", nil); err != nil {
				t.Error(err)
			}
		}
		if !joined {
			<-done
		}
	}()
	// Observe actual journal/entry publication, not a production test hook.
	// The independent native DB writer prevents the final live image update.
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	var journal *smartlistJournal
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for journal == nil {
		select {
		case got := <-done:
			joined = true
			t.Fatalf("Apply finished before entry publication: %+v %v", got.result, got.err)
		case <-ctx.Done():
			t.Fatal("Apply never published its first native entry")
		case <-ticker.C:
			candidate, err := readSmartlistJournal(ctx, active)
			if err != nil || len(candidate.Images) < 2 {
				continue
			}
			first := candidate.Images[0]
			if hash, err := smartlistJournalHash(ctx, filepath.Join(lib, first.Target), false); err == nil && hash == first.StagedHash {
				journal = candidate
			}
		}
	}
	stop()
	if err := sqlitex.ExecuteTransient(locked, "ROLLBACK", nil); err != nil {
		t.Fatal(err)
	}
	transaction = false
	var got outcome
	select {
	case got = <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("Apply failed to finish its rollback")
	}
	if got.err == nil || got.result != nil {
		t.Fatalf("cancelled Apply outcome: %+v %v", got.result, got.err)
	}
	if err := validateSmartlistTargets(context.WithoutCancel(ctx), lib, active, *journal, false); err != nil {
		t.Fatalf("Apply left published targets behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(active, "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("successful rollback retained pending intent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(active, "rolled-back.json")); err != nil {
		t.Fatal(err)
	}
}
