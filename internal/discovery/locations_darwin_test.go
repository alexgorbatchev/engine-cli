package discovery

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := 0
	roots, warnings := darwinLocations(func(buf []unix.Statfs_t, flags int) (int, error) {
		calls++
		if flags != unix.MNT_NOWAIT {
			t.Fatal("mount listing must not refresh remote volumes")
		}
		if buf == nil {
			return 2, nil
		}
		buf[0].Flags = unix.MNT_LOCAL
		copy(buf[0].Mntonname[:], "/Volumes/DJ USB")
		copy(buf[1].Mntonname[:], "/Volumes/Network")
		return 2, nil
	})
	if calls != 2 || len(warnings) != 0 || len(roots) != 2 {
		t.Fatalf("locations %+v warnings %+v calls %d", roots, warnings, calls)
	}
	if roots[0].Path != filepath.Join(home, "Music") || roots[0].Name != "Local library" || roots[1].Name != "Drive: DJ USB" {
		t.Fatalf("picker names: %+v", roots)
	}
	if root := driveLocation("/"); root.Name != "Local disk" {
		t.Fatal(root)
	}
}

func TestDarwinLocationFailures(t *testing.T) {
	t.Setenv("HOME", "")
	for _, failure := range []string{"size", "read", "changed"} {
		t.Run(failure, func(t *testing.T) {
			roots, warnings := darwinLocations(func(buf []unix.Statfs_t, _ int) (int, error) {
				if buf == nil {
					if failure == "size" {
						return 0, errors.New("size failed")
					}
					return 1, nil
				}
				if failure == "changed" {
					return len(buf), nil
				}
				return 0, errors.New("read failed")
			})
			if len(roots) != 0 || len(warnings) != 2 {
				t.Fatalf("failures: %+v %+v", roots, warnings)
			}
		})
	}
}
