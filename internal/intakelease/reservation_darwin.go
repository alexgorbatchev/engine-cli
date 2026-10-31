package intakelease

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func matchingReservationNames(parent, anchors *os.Root) error {
	read := func(root *os.Root) (_ unix.Statfs_t, err error) {
		file, err := root.Open(".")
		if err != nil {
			return unix.Statfs_t{}, err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		var info unix.Statfs_t
		err = unix.Fstatfs(int(file.Fd()), &info)
		runtime.KeepAlive(file)
		return info, err
	}
	a, err := read(parent)
	if err != nil {
		return err
	}
	b, err := read(anchors)
	if err != nil {
		return err
	}
	// These native filesystems choose name comparison at the volume level.
	// FAT/exFAT are supported Engine removable-media formats, not exceptional
	// failures of the per-directory case-sensitivity interfaces.
	name := unix.ByteSliceToString(a.Fstypename[:])
	if a.Fsid != b.Fsid || name != "apfs" && name != "hfs" && name != "msdos" && name != "exfat" {
		return errors.New("intake path coordination requires one supported native volume")
	}
	return nil
}
