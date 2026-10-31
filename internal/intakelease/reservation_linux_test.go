package intakelease

import (
	"errors"
	"os"
	"testing"
)

func TestReservationNamePolicyRefusesClosedNativeRoot(t *testing.T) {
	for _, closed := range []int{0, 1} {
		t.Run([]string{"parent", "anchors"}[closed], func(t *testing.T) {
			owner := &reservation{}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			})
			for range 2 {
				root, err := os.OpenRoot(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				owner.roots = append(owner.roots, root)
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
