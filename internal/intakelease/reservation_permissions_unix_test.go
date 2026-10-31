//go:build !windows

package intakelease

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReservationRefusesSymlinkCoordinatorWithoutFollowingIt(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(parent, reservationDirectory)); err != nil {
		t.Fatal(err)
	}
	lease, err := Observe(t.Context(), filepath.Join(parent, "library"))
	if lease != nil || err == nil {
		t.Fatalf("symlink coordinator accepted: %v %v", lease, err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("coordination escaped into unrelated directory: %v %v", entries, err)
	}
}

func TestReservationPermissionRefusalReleasesEarlierPaths(t *testing.T) {
	for _, kind := range []string{"parent", "read-only-parent", "coordinator", "anchor"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			good, blocked := filepath.Join(base, "a"), filepath.Join(base, "z", "library")
			if err := os.Mkdir(filepath.Dir(blocked), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Dir(blocked)
			if kind != "parent" && kind != "read-only-parent" {
				path = filepath.Join(path, reservationDirectory)
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			mode := os.FileMode(0700)
			if kind == "anchor" {
				path = filepath.Join(path, "library")
				mode = 0600
				if err := os.WriteFile(path, nil, mode); err != nil {
					t.Fatal(err)
				}
			}
			blockedMode := os.FileMode(0400)
			if kind == "read-only-parent" {
				blockedMode = 0500
			}
			if err := os.Chmod(path, blockedMode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(path, mode); err != nil {
					t.Error(err)
				}
			})
			lease, err := Replace(t.Context(), good, blocked)
			if lease != nil || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("protected %s acquired ownership: %v %v", kind, lease, err)
			}
			// The lexically earlier path was acquired first. Failure on the
			// second must release it, not strand another library's exclusion.
			available, err := Replace(t.Context(), good)
			if err != nil {
				t.Fatalf("permission refusal leaked earlier ownership: %v", err)
			}
			if err := available.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			recovered, err := Replace(t.Context(), good, blocked)
			if err != nil {
				t.Fatalf("restored permissions could not reacquire both paths: %v", err)
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
