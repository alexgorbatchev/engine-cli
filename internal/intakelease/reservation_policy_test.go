package intakelease

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReservationNamePolicyRefusesClosedNativeRoot(t *testing.T) {
	for _, closed := range []int{0, 1} {
		t.Run([]string{"parent", "anchors"}[closed], func(t *testing.T) {
			parent := t.TempDir()
			owner := &reservation{}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			})
			anchors := filepath.Join(parent, reservationDirectory)
			if err := os.Mkdir(anchors, 0700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{parent, anchors} {
				root, err := os.OpenRoot(path)
				if err != nil {
					t.Fatal(err)
				}
				owner.roots = append(owner.roots, root)
			}
			if err := matchingReservationNames(owner.roots[0], owner.roots[1]); err != nil {
				t.Fatalf("ordinary open native roots refused: %v", err)
			}
			if err := owner.roots[closed].Close(); err != nil {
				t.Fatal(err)
			}
			if err := matchingReservationNames(owner.roots[0], owner.roots[1]); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("unavailable native naming policy did not refuse ownership: %v", err)
			}
		})
	}
}
