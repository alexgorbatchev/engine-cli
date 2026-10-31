package intakelease

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// FS_CASEFOLD_FL is the kernel's per-directory case-insensitive lookup flag.
const casefoldFlag = 0x40000000

func matchingReservationNames(parent, anchors *os.Root) error {
	read := func(root *os.Root) (_ unix.Statfs_t, _ int, err error) {
		file, err := root.Open(".")
		if err != nil {
			return unix.Statfs_t{}, 0, err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		var volume unix.Statfs_t
		if err := unix.Fstatfs(int(file.Fd()), &volume); err != nil {
			return volume, 0, err
		}
		// Native FAT lookup is mount-wide; exFAT uses its volume upcase
		// table. Older kernels need not expose either through GETFLAGS.
		if volume.Type == unix.MSDOS_SUPER_MAGIC || volume.Type == unix.EXFAT_SUPER_MAGIC {
			runtime.KeepAlive(file)
			return volume, 0, nil
		}
		flags, err := unix.IoctlGetInt(int(file.Fd()), unix.FS_IOC_GETFLAGS)
		runtime.KeepAlive(file)
		return volume, flags & casefoldFlag, err
	}
	av, a, err := read(parent)
	if err != nil {
		return err
	}
	bv, b, err := read(anchors)
	if err != nil {
		return err
	}
	if av.Type != bv.Type || av.Fsid != bv.Fsid || a != b {
		return errors.New("intake path coordination has different case sensitivity from its parent")
	}
	return nil
}
