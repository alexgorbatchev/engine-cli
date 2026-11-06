package backup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const restoreChildLibrary = "ENGINE_WATCH_RESTORE_CHILD_LIBRARY"
const restoreChildSnapshot = "ENGINE_WATCH_RESTORE_CHILD_SNAPSHOT"
const restoreCheckpoint = "watch-restore-database-published"

func TestWatchRestoreInterruptedChild(t *testing.T) {
	library := os.Getenv(restoreChildLibrary)
	if library == "" {
		return
	}
	ctx := progress.WithReporter(t.Context(), func(event progress.Event) {
		if event.Phase != "restore.root" {
			return
		}
		// This public progress phase follows the Engine database publication,
		// while the selected operational pair and its portable marker are pending.
		if _, err := fmt.Fprintln(os.Stdout, restoreCheckpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
	})
	_, err := history.Run(ctx, library, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
		return RestoreBackup(ctx, os.Getenv(restoreChildSnapshot), library)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWatchInterruptedRestoreRetainsPairAndAuthority(t *testing.T) {
	library := createDummyEngineLibrary(t)
	queue, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	state, err := queue.WatchAdd(t.Context(), intake.WatchAddOptions{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateBackup(t.Context(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	enginePath := filepath.Join(library, "Database2", "m.db")
	selectedEngine, err := history.DatabaseFingerprint(t.Context(), enginePath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(enginePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE fixture SET value='before interrupted restore'"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	watchPath := filepath.Join(library, ".engine-cli", companion.WatchName)
	watchBefore, err := history.DatabaseFingerprint(t.Context(), watchPath)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWatchRestoreInterruptedChild$", "-test.v")
	child.Env = append(os.Environ(), restoreChildLibrary+"="+library, restoreChildSnapshot+"="+snapshot.Path)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	scanner := bufio.NewScanner(output)
	reached := false
	for scanner.Scan() {
		if scanner.Text() == restoreCheckpoint {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatalf("child did not reach public publication phase: %v", scanner.Err())
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("interrupted restore child completed successfully")
	}
	joined = true
	publishedEngine, err := history.DatabaseFingerprint(t.Context(), enginePath)
	if err != nil || publishedEngine != selectedEngine {
		t.Fatalf("child did not publish selected Engine image: %s %s %v", selectedEngine, publishedEngine, err)
	}
	watchAfter, err := history.DatabaseFingerprint(t.Context(), watchPath)
	if err != nil || watchAfter != watchBefore {
		t.Fatalf("checkpoint already replaced portable control: %s %s %v", watchBefore, watchAfter, err)
	}
	stateAfter, err := intake.WatchList(t.Context(), library)
	if err != nil || !stateAfter.Suspended || stateAfter.Reason != "restored" || stateAfter.Revision <= state.Revision {
		t.Fatalf("kill before portable marker lost native suspension: %+v %v", stateAfter, err)
	}
	entries, err := history.List(library)
	if err != nil || len(entries) != 1 || entries[0].After != nil {
		t.Fatalf("interrupted publication recovery missing: %+v %v", entries, err)
	}
	for _, name := range []string{companion.IntakeName, companion.WatchName} {
		if _, ok := entries[0].Before.Databases[filepath.Join(library, ".engine-cli", name)]; !ok {
			t.Fatalf("interrupted entry omitted pair Before image: %s", name)
		}
	}
	// A real forced recovery restores both enrolled originals, then fences them
	// again rather than implicitly authorizing automatic discovery.
	if _, err := history.Undo(t.Context(), library, entries[0].ID, history.UndoOptions{Force: true}); err != nil {
		t.Fatal("recover interrupted pair:", err)
	}
	d, err = db.Open(enginePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var value string
	if err := d.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "before interrupted restore" {
		t.Fatalf("native recovery changed original collection: %q %v", value, err)
	}
	recovered, err := intake.WatchList(t.Context(), library)
	if err != nil || !recovered.Suspended || recovered.Revision <= stateAfter.Revision {
		t.Fatalf("recovery reused authorization: %+v %v", recovered, err)
	}
}
