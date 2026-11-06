//go:build linux || darwin

package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIncompleteNativeWatchTraversalPreservesObservationUntilRecovery(t *testing.T) {
	s, _ := queueFixture(t)
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "source.wav"), []byte("native source remains unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	scan, _, err := s.reconcileWatch(t.Context(), time.Hour)
	if err != nil || scan.Pending != 1 || scan.Queued != 0 {
		t.Fatalf("initial quiet observation: %+v %v", scan, err)
	}
	d, err := openWatch(t.Context(), s.library)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	var version, stable int64
	if err := d.QueryRowContext(t.Context(), "SELECT version_id,stable_since FROM WatchAlias JOIN WatchVersion ON WatchVersion.id=WatchAlias.version_id").Scan(&version, &stable); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(nested, 0700); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.ReadDir(nested); !errors.Is(err, os.ErrPermission) {
		t.Skipf("native directory permissions do not deny this process: %v", err)
	}
	scan, _, err = s.reconcileWatch(t.Context(), 0)
	if err == nil || scan.Completed || scan.Queued != 0 {
		t.Fatalf("incomplete traversal published: %+v %v", scan, err)
	}
	var currentVersion, currentStable int64
	if err := d.QueryRowContext(t.Context(), "SELECT version_id,stable_since FROM WatchAlias JOIN WatchVersion ON WatchVersion.id=WatchAlias.version_id").Scan(&currentVersion, &currentStable); err != nil || version != currentVersion || stable != currentStable {
		t.Fatalf("failed traversal replaced observation: version=%d stable=%d err=%v", currentVersion, currentStable, err)
	}
	items, err := s.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("incomplete source queued: %+v %v", items, err)
	}
	after, err := s.WatchList(t.Context())
	if err != nil || after.Revision != state.Revision || after.Roots[0].Phase != "degraded" {
		t.Fatalf("failure did not preserve configured authorization: %+v %v", after, err)
	}
	if err := os.Chmod(nested, 0700); err != nil {
		t.Fatal(err)
	}
	scan, _, err = s.reconcileWatch(t.Context(), 0)
	if err != nil || !scan.Completed || scan.Queued != 1 {
		t.Fatalf("native traversal did not recover: %+v %v", scan, err)
	}
	configured, identity, err := s.pinnedWatchRoot(t.Context(), state.Roots[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if sources, _, err := s.scanWatchRoot(ctx, configured, identity); !errors.Is(err, context.Canceled) || len(sources) != 0 {
		t.Fatalf("cancelled traversal produced observations: %+v %v", sources, err)
	}
	if sources, _, err := s.scanWatchRoot(t.Context(), configured, identity+"-stale"); err == nil || len(sources) != 0 {
		t.Fatalf("stale root identity produced observations: %+v %v", sources, err)
	}
	if err := os.Rename(root, root+"-retained"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(root+"-retained", root); err != nil {
			t.Error(err)
		}
	})
	if sources, _, err := s.scanWatchRoot(t.Context(), configured, identity); err == nil || len(sources) != 0 {
		t.Fatalf("missing root produced observations: %+v %v", sources, err)
	}
}
