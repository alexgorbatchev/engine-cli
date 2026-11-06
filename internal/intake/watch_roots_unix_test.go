//go:build linux || darwin

package intake

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatchRootAliasPinsCanonicalTarget(t *testing.T) {
	s, _ := queueFixture(t)
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "selected-music")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: alias, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Roots) != 1 || state.Roots[0].Path != canonical {
		t.Fatalf("root alias not made visible: %+v", state)
	}
	_, err = s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Revision: state.Revision})
	watchCode(t, err, "watch_root_exists")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchActivate(t.Context(), state.Revision); err != nil {
		t.Fatal("retargeted input alias changed persisted canonical authorization:", err)
	}
}
