package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
	"github.com/fsnotify/fsnotify"
)

func TestWatchServicePausePreparationDisableAndRestart(t *testing.T) {
	store, library := queueFixture(t)
	if err := store.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	source := writeImportWAV(t)
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
	scan, err := service.WatchReconcile(t.Context())
	if err != nil || !scan.Completed || scan.Observed != 1 || scan.Pending != 1 || scan.State.Manager != "running" {
		t.Fatalf("paused discovery: %+v %v", scan, err)
	}
	items, err := store.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("paused discovery enqueued media: %+v %v", items, err)
	}
	if err := store.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WatchReconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Process(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(t.Context())
	if err != nil || len(items) != 1 || items[0].Status != "staged" || items[0].HistoryID != "" {
		t.Fatalf("discovery did not prepare without importing: %+v %v", items, err)
	}
	state, err = service.WatchSetEnabled(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(source), "new.wav"), []byte("not imported"), 0600); err != nil {
		t.Fatal(err)
	}
	scan, err = service.WatchReconcile(t.Context())
	if err != nil || !scan.Completed || scan.Roots != 0 || scan.State.Roots[0].Phase != "disabled" {
		t.Fatalf("revoked scan: %+v %v", scan, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = service.WatchReconcile(t.Context())
	watchCode(t, err, "intake_closed")
	service, err = OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	state, err = service.WatchList(t.Context())
	if err != nil || len(state.Roots) != 1 || state.Roots[0].Enabled {
		t.Fatalf("restart changed authorization: %+v %v", state, err)
	}
	if err := service.Process(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.List(t.Context())
	if err != nil || len(after) != 1 || after[0] != items[0] {
		t.Fatalf("restart reset native version: %+v %v", after, err)
	}
}

func TestWatchEventsSynchronizeEmptyAuthorization(t *testing.T) {
	root := t.TempDir()
	dir, err := watchfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	identity, err := dir.Identity()
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err := syncWatchDirectories(watcher, []watchDirectory{{path: dir.Path(), identity: identity}}); err != nil {
		t.Fatal(err)
	}
	if len(watcher.WatchList()) != 1 {
		t.Fatal("authorized native directory not watched")
	}
	if err := syncWatchDirectories(watcher, nil); err != nil {
		t.Fatal(err)
	}
	if len(watcher.WatchList()) != 0 {
		t.Fatal("revoked native event watch remained installed")
	}
}

func TestWatchServiceCancelledRequestDoesNotCloseOwner(t *testing.T) {
	_, library := queueFixture(t)
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.WatchAdd(ctx, WatchAddOptions{Path: t.TempDir()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation: %v", err)
	}
	state, err := service.WatchList(t.Context())
	if err != nil || len(state.Roots) != 0 || state.Revision != 0 {
		t.Fatalf("cancelled request changed owner: %+v %v", state, err)
	}
	state, err = service.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir()})
	if err != nil || state.Revision != 1 {
		t.Fatalf("cancelled caller stopped shared service: %+v %v", state, err)
	}
}

func TestWatchServicePreservesRootFailureAfterEventRegistration(t *testing.T) {
	_, library := queueFixture(t)
	root := filepath.Join(t.TempDir(), "music")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.WatchAdd(t.Context(), WatchAddOptions{Path: root}); err != nil {
		t.Fatal(err)
	}
	if scan, err := service.WatchReconcile(t.Context()); err != nil || !scan.Completed {
		t.Fatalf("initial native event registration: %+v %v", scan, err)
	}
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	scan, err := service.WatchReconcile(t.Context())
	watchCode(t, err, "watch_root_changed")
	if scan.Completed || scan.State.Manager != "degraded" || scan.State.ErrorCode != "watch_root_changed" {
		t.Fatalf("native root failure was relabelled: %+v %v", scan, err)
	}
	if len(scan.State.Roots) != 1 || scan.State.Roots[0].ErrorCode != "watch_root_changed" {
		t.Fatalf("root failure lost: %+v", scan.State)
	}
}

func TestWatchServiceForeignOwnerCannotChangeAuthorization(t *testing.T) {
	_, library := queueFixture(t)
	first, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	state, err := first.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.WatchReconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	opts := WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}
	_, err = second.WatchSetEnabled(t.Context(), opts, false)
	watchCode(t, err, "watch_owned")
	after, err := first.WatchList(t.Context())
	if err != nil || after.Revision != state.Revision || !after.Roots[0].Enabled {
		t.Fatalf("foreign discovery owner revoked live authorization: %+v %v", after, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	after, err = second.WatchSetEnabled(t.Context(), opts, false)
	if err != nil || after.Revision != state.Revision+1 || after.Roots[0].Enabled {
		t.Fatalf("closed discovery owner did not release native ownership: %+v %v", after, err)
	}
}
