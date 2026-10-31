package intakelease

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReservationInvalidLocationDoesNotEnrollStorage(t *testing.T) {
	for _, kind := range []string{"missing-parent", "file-parent", "root", "coordinator"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "missing", "library")
			switch kind {
			case "file-parent":
				if err := os.WriteFile(filepath.Join(base, "missing"), []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "root":
				path = filepath.VolumeName(base) + string(filepath.Separator)
			case "coordinator":
				path = filepath.Join(base, reservationDirectory)
			}
			if lease, err := Observe(t.Context(), path); lease != nil || err == nil {
				t.Fatalf("invalid location acquired ownership: %v %v", lease, err)
			}
			if _, err := os.Lstat(filepath.Join(base, reservationDirectory)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid location enrolled coordination: %v", err)
			}
		})
	}
}

func TestReservationRefusesCoordinatorFile(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, reservationDirectory)
	want := []byte("unrelated retained file")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	if lease, err := Observe(t.Context(), filepath.Join(base, "library")); lease != nil || err == nil {
		t.Fatalf("ordinary file acquired directory ownership: %v %v", lease, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("failed ownership changed unrelated file: %q %v", got, err)
	}
}

func TestReservationCloseContinuesAfterNativeCloseFailure(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	owner, err := reservePaths(t.Context(), true, a, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	if len(owner.files) != 2 {
		t.Fatalf("two paths did not retain two native files: %d", len(owner.files))
	}
	// The test owns this file exclusively. A failed cleanup must not strand the
	// other path's lock or prevent subsequent attempts from closing cleanly.
	if err := owner.files[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("native close failure lost: %v", err)
	}
	next, err := Replace(t.Context(), a, b)
	if err != nil {
		t.Fatalf("cleanup failure stranded another path: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}
