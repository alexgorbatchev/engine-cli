//go:build linux || darwin

package track

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestKeepOwnerCopiesTheOriginalGroup(t *testing.T) {
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	original, replacement := filepath.Join(dir, "a.mp3"), filepath.Join(dir, "b.mp3")
	for _, path := range []string{original, replacement} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	own := info.Sys().(*syscall.Stat_t)
	other := -1
	for _, group := range groups {
		if uint32(group) != own.Gid {
			other = group
			break
		}
	}
	if other >= 0 {
		if err := os.Lchown(original, -1, other); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := keepOwner(replacement, before); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.Sys().(*syscall.Stat_t), before.Sys().(*syscall.Stat_t); got.Uid != want.Uid || got.Gid != want.Gid {
		t.Fatalf("owner %d:%d want %d:%d", got.Uid, got.Gid, want.Uid, want.Gid)
	}
	if err := keepOwner(filepath.Join(dir, "missing.mp3"), before); err == nil {
		t.Fatal("kept the owner of a missing file")
	}
}
