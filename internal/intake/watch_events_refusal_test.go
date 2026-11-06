package intake

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/watchclock"
)

func TestWatchEventsRejectSameCardinalityWrongBackend(t *testing.T) {
	manager, ctx, events := watchEventOwner(t)
	if changed, err := manager.syncWatchDirectories(ctx, events, nil); err != nil || changed || *events != nil {
		t.Fatalf("empty authorization allocated native ownership: %v %v", changed, err)
	}
	entry := watchEventDirectory(t, t.TempDir())
	if _, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil {
		t.Fatal(err)
	}
	old := *events
	if err := old.watcher.Remove(entry.path); err != nil {
		t.Fatal(err)
	}
	if err := old.watcher.Add(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(old.watcher.WatchList()) != 1 {
		t.Fatal("native backend did not retain the unrelated directory")
	}
	if changed, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil || !changed || *events == old {
		t.Fatalf("same-cardinality unrelated registration accepted: %v %v", changed, err)
	}
	select {
	case <-old.done:
	default:
		t.Fatal("wrong-backend replacement did not join previous drain")
	}
	if !watchRegistrationsPresent(*events) || (*events).watcher.WatchList()[0] != entry.path {
		t.Fatal("replacement did not restore authorized native coverage")
	}
}

func TestWatchServiceRestoredActivationAndRemovalPreserveIntake(t *testing.T) {
	store, library := queueFixture(t)
	if err := store.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	source := writeImportWAV(t)
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := service.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := service.WatchAdd(t.Context(), WatchAddOptions{Path: filepath.Dir(source), Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	err = intakestate.With(t.Context(), library, func(ctx context.Context) error {
		_, err := watchclock.Restore(ctx, library, state.Revision)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := service.WatchList(t.Context())
	if err != nil || !restored.Suspended || restored.Revision <= state.Revision {
		t.Fatalf("restored authority was not visible: %+v %v", restored, err)
	}
	_, err = service.WatchReconcile(t.Context())
	watchCode(t, err, "watch_suspended")
	_, err = service.WatchActivate(t.Context(), state.Revision)
	watchCode(t, err, "watch_revision_changed")
	active, err := service.WatchActivate(t.Context(), restored.Revision)
	if err != nil || active.Suspended || active.Reason != "" {
		t.Fatalf("explicit current activation failed: %+v %v", active, err)
	}
	removed, err := service.WatchRemove(t.Context(), WatchRootOptions{ID: active.Roots[0].ID, Revision: active.Revision})
	if err != nil || len(removed.Roots) != 0 {
		t.Fatalf("current removal failed: %+v %v", removed, err)
	}
	scan, err := service.WatchReconcile(t.Context())
	if err != nil || !scan.Completed || scan.Roots != 0 {
		t.Fatalf("removed authorization remained active: %+v %v", scan, err)
	}
	items, err := store.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("paused lifecycle changed retained intake rows: %+v %v", items, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("authorization lifecycle changed original audio: %v", err)
	}
}
