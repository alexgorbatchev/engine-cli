package intake

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func TestWatchActivationRefusesUnavailableRootWithoutLosingAuthorization(t *testing.T) {
	s, _ := queueFixture(t)
	path := filepath.Join(t.TempDir(), "music")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: path, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	_, err = s.WatchActivate(t.Context(), state.Revision)
	watchCode(t, err, "watch_root_unavailable")
	after, err := s.WatchList(t.Context())
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatalf("unavailable root changed authorization: %+v %v", after, err)
	}
	if err := os.Rename(path+"-original", path); err != nil {
		t.Fatal(err)
	}
	state, err = s.WatchActivate(t.Context(), state.Revision)
	if err != nil {
		t.Fatal("activation did not recover:", err)
	}
	retiredID := state.Roots[0].ID
	state, err = s.WatchRemove(t.Context(), WatchRootOptions{ID: retiredID, Revision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"enable", "remove"} {
		var err error
		if operation == "enable" {
			_, err = s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: retiredID, Revision: state.Revision}, true)
		} else {
			_, err = s.WatchRemove(t.Context(), WatchRootOptions{ID: retiredID, Revision: state.Revision})
		}
		watchCode(t, err, "watch_revision_changed")
	}
	after, err = s.WatchList(t.Context())
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatalf("missing-root operation changed state: %+v %v", after, err)
	}
}

func TestNativeEventRegistrationRefusesStaleTraversalAndRecovers(t *testing.T) {
	root := t.TempDir()
	dir, err := watchfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	root = dir.Path()
	if closeErr := dir.Close(); err != nil || closeErr != nil {
		t.Fatalf("root identity: %v %v", err, closeErr)
	}
	manager := newWatchManager()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var events *watchEvents
	closed := false
	t.Cleanup(func() {
		if !closed && events != nil {
			if err := events.close(); err != nil {
				t.Error(err)
			}
		}
	})
	for _, entry := range []watchDirectory{{path: root + "-absent", identity: identity}, {path: root, identity: identity + "-different"}} {
		if _, err := manager.syncWatchDirectories(ctx, &events, []watchDirectory{entry}); err == nil {
			t.Fatal("stale traversal registered native notifications")
		}
		if events != nil {
			t.Fatal("refused traversal left native event owner")
		}
	}
	entry := []watchDirectory{{path: root, identity: identity}}
	if _, err := manager.syncWatchDirectories(ctx, &events, entry); err != nil {
		t.Fatal("registration did not recover:", err)
	}
	if got := events.watcher.WatchList(); len(got) != 1 || got[0] != root {
		t.Fatalf("native watch missing: %v", got)
	}
	if _, err := manager.syncWatchDirectories(ctx, &events, nil); err != nil {
		t.Fatal(err)
	}
	if events != nil {
		t.Fatal("obsolete native event owner retained")
	}
	cancel()
	closed = true
	if _, err := manager.syncWatchDirectories(ctx, &events, entry); err == nil {
		t.Fatal("cancelled native event source reported successful registration")
	}
}
