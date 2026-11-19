//go:build linux || darwin

package track

import (
	"os"
	"syscall"
)

// keepOwner gives the replacement the original's owner and group when they differ.
func keepOwner(path string, original os.FileInfo) error {
	want, ok := original.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	have := info.Sys().(*syscall.Stat_t)
	if have.Uid == want.Uid && have.Gid == want.Gid {
		return nil
	}
	return os.Lchown(path, int(want.Uid), int(want.Gid))
}
