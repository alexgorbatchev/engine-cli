//go:build !windows

package intakelease

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReservationRefusesReplacedCoordinationRoots(t *testing.T) {
	for _, replacement := range []string{"parent", "anchors"} {
		t.Run(replacement, func(t *testing.T) {
			base := t.TempDir()
			parentPath := filepath.Join(base, "parent")
			anchorsPath := filepath.Join(parentPath, reservationDirectory)
			if err := os.MkdirAll(anchorsPath, 0700); err != nil {
				t.Fatal(err)
			}
			parent, err := os.OpenRoot(parentPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := parent.Close(); err != nil {
					t.Error(err)
				}
			}()
			anchors, err := parent.OpenRoot(reservationDirectory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := anchors.Close(); err != nil {
					t.Error(err)
				}
			}()
			library := filepath.Join(parentPath, "library")
			if err := checkReservationRoots(library, parent, anchors); err != nil {
				t.Fatalf("unchanged native roots refused: %v", err)
			}
			changed := anchorsPath
			if replacement == "parent" {
				changed = parentPath
			}
			if err := os.Rename(changed, changed+"-retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(anchorsPath, 0700); err != nil {
				t.Fatal(err)
			}
			if err := checkReservationRoots(library, parent, anchors); err == nil {
				t.Fatal("replacement directory accepted as retained coordination owner")
			}
		})
	}
}
