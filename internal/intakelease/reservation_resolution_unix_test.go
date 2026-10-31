//go:build !windows

package intakelease

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReservationRefusesUnresolvableLibraryWithoutEnrollment(t *testing.T) {
	for _, kind := range []string{"symlink-loop", "unsearchable-parent", "unreadable-parent", "unreadable-library"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			parent := filepath.Join(base, "parent")
			library := filepath.Join(parent, "library")
			if err := os.MkdirAll(library, 0700); err != nil {
				t.Fatal(err)
			}
			blocked, mode := parent, os.FileMode(0400)
			switch kind {
			case "symlink-loop":
				library = filepath.Join(parent, "loop")
				if err := os.Symlink("loop", library); err != nil {
					t.Fatal(err)
				}
			case "unreadable-parent":
				mode = 0111
			case "unreadable-library":
				blocked, mode = library, 0111
			}
			if kind != "symlink-loop" {
				if err := os.Chmod(blocked, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(blocked, 0700); err != nil {
						t.Error(err)
					}
				})
			}
			lease, err := Observe(t.Context(), library)
			if lease != nil {
				if closeErr := lease.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("unresolvable library acquired ownership")
			}
			if err == nil || kind != "symlink-loop" && !errors.Is(err, os.ErrPermission) {
				t.Fatalf("native resolution failure lost: %v", err)
			}
			if err := os.Chmod(blocked, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(parent, reservationDirectory)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("resolution failure enrolled ownership storage: %v", err)
			}
			if kind == "symlink-loop" {
				if err := os.Remove(library); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err := Replace(t.Context(), library)
			if err != nil {
				t.Fatalf("repaired native path could not acquire ownership: %v", err)
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReservationDoesNotInspectInaccessibleSiblingLibrary(t *testing.T) {
	parent := t.TempDir()
	selected, unrelated := filepath.Join(parent, "selected"), filepath.Join(parent, "unrelated")
	for _, path := range []string{selected, unrelated} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(unrelated, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unrelated, 0700); err != nil {
			t.Error(err)
		}
	})
	lease, err := Observe(t.Context(), selected)
	if err != nil {
		t.Fatalf("unrelated inaccessible library blocked selected ownership: %v", err)
	}
	assertReserved(t, selected)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(parent, reservationDirectory, "unrelated")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected ownership enrolled unrelated library: %v", err)
	}
}

func TestReservationRootChecksRefuseUnavailableStorage(t *testing.T) {
	for _, removal := range []string{"parent", "anchors", "closed-parent", "closed-anchors"} {
		t.Run(removal, func(t *testing.T) {
			base := t.TempDir()
			parentPath := filepath.Join(base, "parent")
			anchorsPath := filepath.Join(parentPath, reservationDirectory)
			if err := os.MkdirAll(anchorsPath, 0700); err != nil {
				t.Fatal(err)
			}
			owner := &reservation{}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			})
			parent, err := os.OpenRoot(parentPath)
			if err != nil {
				t.Fatal(err)
			}
			owner.roots = append(owner.roots, parent)
			anchors, err := parent.OpenRoot(reservationDirectory)
			if err != nil {
				t.Fatal(err)
			}
			owner.roots = append(owner.roots, anchors)
			library := filepath.Join(parentPath, "library")
			if err := checkReservationRoots(library, parent, anchors); err != nil {
				t.Fatal(err)
			}
			switch removal {
			case "parent":
				err = os.Rename(parentPath, parentPath+"-retained")
			case "anchors":
				err = os.Rename(anchorsPath, anchorsPath+"-retained")
			case "closed-parent":
				err = parent.Close()
			case "closed-anchors":
				err = anchors.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := checkReservationRoots(library, parent, anchors); err == nil {
				t.Fatal("unavailable retained roots passed ownership validation")
			}
			if _, err := os.Lstat(filepath.Join(anchorsPath, "library")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("root validation enrolled replacement storage: %v", err)
			}
		})
	}
}

func TestReservationResolutionPropagatesNativeLookupFailures(t *testing.T) {
	parent := t.TempDir()
	for _, lookup := range []struct {
		name  string
		path  string
		child string
	}{
		{"missing-parent", filepath.Join(parent, "missing"), "library"},
		{"invalid-entry", parent, "invalid\x00entry"},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			name, err := reservationName(lookup.path, lookup.child)
			if err == nil || name != "" {
				t.Fatalf("failed native lookup produced reservation name %q: %v", name, err)
			}
		})
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	if identity, err := reservationEntryIdentity(root, "missing"); identity != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory produced identity %q: %v", identity, err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if identity, err := rootIdentity(root); identity != "" || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed directory produced identity %q: %v", identity, err)
	}
}
