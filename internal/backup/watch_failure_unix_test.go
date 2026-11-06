//go:build linux || darwin

package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestWatchUndoPublicationFailureRetainsPendingPair(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses native file write permissions")
	}
	library := createDummyEngineLibrary(t)
	queue, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	state, err := queue.WatchAdd(t.Context(), intake.WatchAddOptions{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateBackup(t.Context(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.WatchSetEnabled(t.Context(), intake.WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(t.Context(), library, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
		return RestoreBackup(ctx, snapshot.Path, library)
	})
	if err != nil {
		t.Fatal(err)
	}
	watch := filepath.Join(library, ".engine-cli", companion.WatchName)
	if err := os.Chmod(watch, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(watch, 0600) })
	failed, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{})
	if progress.ErrorCode(err) != "history_recovery_required" || failed == nil || failed.After != nil {
		t.Fatalf("failed pair publication was not retained pending: %+v %v", failed, err)
	}
	if _, err := history.Run(t.Context(), library, "must.not.run", history.ScopeCollection, nil, func(context.Context) error {
		t.Fatal("mutation crossed pending pair recovery")
		return nil
	}); progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("pending pair did not block later mutation: %v", err)
	}
	if err := os.Chmod(watch, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Undo(t.Context(), library, failed.ID, history.UndoOptions{Force: true}); err != nil {
		t.Fatal("recover failed pair Undo:", err)
	}
	recovered, err := intake.WatchList(t.Context(), library)
	if err != nil || !recovered.Suspended || len(recovered.Roots) != 1 || !recovered.Roots[0].Enabled {
		t.Fatalf("forced recovery lost pre-Undo configuration: %+v %v", recovered, err)
	}
}
