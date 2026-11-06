package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func watchEventDirectory(t *testing.T, path string) watchDirectory {
	t.Helper()
	dir, err := watchfs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	if err := errors.Join(err, dir.Close()); err != nil {
		t.Fatal(err)
	}
	return watchDirectory{path: path, identity: identity}
}

func watchEventOwner(t *testing.T) (*watchManager, context.Context, **watchEvents) {
	t.Helper()
	manager := newWatchManager()
	ctx, cancel := context.WithCancel(t.Context())
	var events *watchEvents
	t.Cleanup(func() {
		cancel()
		if events != nil {
			if err := events.close(); err != nil {
				t.Error(err)
			}
		}
	})
	return manager, ctx, &events
}

func TestWatchEventsReplaceSamePathNativeDirectory(t *testing.T) {
	manager, ctx, events := watchEventOwner(t)
	path := filepath.Join(t.TempDir(), "child")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{watchEventDirectory(t, path)}); err != nil || !changed {
		t.Fatalf("initial registration: %v %v", changed, err)
	}
	old := *events
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{watchEventDirectory(t, path)}); err != nil || !changed || *events == old {
		t.Fatalf("same-path replacement retained old native coverage: %v %v", changed, err)
	}
	select {
	case <-old.done:
	default:
		t.Fatal("replaced native drain was not joined")
	}
	select {
	case <-manager.wake:
	default:
	}
	if err := os.WriteFile(filepath.Join(path, "event.wav"), []byte("native notification"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-manager.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement directory did not deliver native events")
	}
}

func TestWatchEventsRetainAliasRepresentative(t *testing.T) {
	manager, ctx, events := watchEventOwner(t)
	parent := t.TempDir()
	path := filepath.Join(parent, "z-directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	entry := watchEventDirectory(t, path)
	if _, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil {
		t.Fatal(err)
	}
	old := *events
	alias := filepath.Join(parent, "a-alias")
	if err := os.Symlink(path, alias); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			t.Skip("native symlink creation requires Windows privilege")
		}
		t.Fatal(err)
	}
	aliased := watchEventDirectory(t, alias)
	for _, entries := range [][]watchDirectory{{aliased, entry}, {entry, aliased}} {
		if changed, err := manager.syncWatchDirectories(ctx, events, entries); err != nil || changed || *events != old {
			t.Fatalf("alias ordering replaced stable representative: %v %v", changed, err)
		}
	}
}

func TestWatchEventsRepairMissingBackend(t *testing.T) {
	manager, ctx, events := watchEventOwner(t)
	entry := watchEventDirectory(t, t.TempDir())
	if _, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil {
		t.Fatal(err)
	}
	old := *events
	path := entry.path
	if err := old.watcher.Remove(path); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil || !changed || *events == old {
		t.Fatalf("missing backend registration was not repaired: %v %v", changed, err)
	}
	select {
	case <-old.done:
	default:
		t.Fatal("missing backend replacement did not join old drain")
	}
	old = *events
	if err := old.watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil || !changed || *events == old {
		t.Fatalf("closed backend was not replaced: %v %v", changed, err)
	}
	select {
	case <-old.done:
	default:
		t.Fatal("closed backend drain was not joined")
	}
}

func TestWatchEventsRefusalRetainsOwnerAndEmptySetJoins(t *testing.T) {
	manager, ctx, events := watchEventOwner(t)
	entry := watchEventDirectory(t, t.TempDir())
	if _, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry}); err != nil {
		t.Fatal(err)
	}
	old := *events
	stale := entry
	stale.identity += "-stale"
	if changed, err := manager.syncWatchDirectories(ctx, events, []watchDirectory{entry, stale}); err == nil || changed || *events != old {
		t.Fatalf("refused candidate replaced successful owner: %v %v", changed, err)
	}
	if !watchRegistrationsPresent(old) {
		t.Fatal("refused candidate damaged installed coverage")
	}
	if changed, err := manager.syncWatchDirectories(ctx, events, nil); err != nil || changed || *events != nil {
		t.Fatalf("empty authorization retained events or requested follow-up: %v %v", changed, err)
	}
	select {
	case <-old.done:
	default:
		t.Fatal("empty authorization returned before native drain joined")
	}
	if len(old.watcher.WatchList()) != 0 {
		t.Fatal("empty authorization left native registrations")
	}
}
