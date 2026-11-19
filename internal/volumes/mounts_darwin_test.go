package volumes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func statfs(path string, flags uint32) unix.Statfs_t {
	var s unix.Statfs_t
	copy(s.Mntonname[:], path)
	s.Flags = flags
	return s
}

func TestDarwinMountsKeepTheSystemVolumeAndAttachedDrives(t *testing.T) {
	stats := []unix.Statfs_t{statfs("/", unix.MNT_LOCAL), statfs("/System/Volumes/Data", unix.MNT_LOCAL), statfs("/Volumes/DJ_USB", unix.MNT_LOCAL), statfs("/Volumes/Recovery", unix.MNT_DONTBROWSE)}
	read := func(buf []unix.Statfs_t, _ int) (int, error) {
		return copy(buf, stats), nil
	}
	got, err := darwinMounts(read, "Macintosh HD")
	if err != nil || len(got) != 2 || got[0] != (mount{path: "/", name: "Macintosh HD"}) || got[1] != (mount{path: "/Volumes/DJ_USB", removable: true}) {
		t.Fatalf("mounts=%+v err=%v", got, err)
	}
	if _, err := darwinMounts(func([]unix.Statfs_t, int) (int, error) { return 0, errors.New("denied") }, ""); err == nil {
		t.Fatal("read failure ignored")
	}
	calls := 0
	growing := func(buf []unix.Statfs_t, _ int) (int, error) {
		calls++
		if calls == 1 {
			return 1, nil
		}
		return len(buf), nil
	}
	if _, err := darwinMounts(growing, ""); err == nil {
		t.Fatal("a changing drive list was accepted")
	}
	failing := func(buf []unix.Statfs_t, _ int) (int, error) {
		if buf == nil {
			return 1, nil
		}
		return 0, errors.New("denied")
	}
	if _, err := darwinMounts(failing, ""); err == nil {
		t.Fatal("second read failure ignored")
	}
}

func TestSystemNameFollowsTheLinkToRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/", filepath.Join(dir, "Studio SSD")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "DJ_USB"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := systemName(dir); got != "Studio SSD" {
		t.Fatalf("name=%q", got)
	}
	if got := systemName(filepath.Join(dir, "missing")); got != "This computer" {
		t.Fatalf("fallback=%q", got)
	}
	if got := systemName(filepath.Join(dir, "DJ_USB")); got != "This computer" {
		t.Fatalf("no link=%q", got)
	}
}
