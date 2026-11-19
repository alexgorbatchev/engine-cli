package volumes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func mounted() ([]mount, error) {
	return darwinMounts(unix.Getfsstat, systemName("/Volumes"))
}

// darwinMounts keeps the system volume and drives mounted under /Volumes, which
// is where macOS attaches USB sticks, SD cards and external disks.
func darwinMounts(read func([]unix.Statfs_t, int) (int, error), rootName string) ([]mount, error) {
	n, err := read(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	// Leave room for drives attached between the sizing call and the read.
	stats := make([]unix.Statfs_t, n+16)
	n, err = read(stats, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	if n >= len(stats) {
		return nil, fmt.Errorf("mounted drive list changed; list drives again")
	}
	var mounts []mount
	for _, stat := range stats[:n] {
		path := unix.ByteSliceToString(stat.Mntonname[:])
		switch {
		case path == "/":
			mounts = append(mounts, mount{path: path, name: rootName})
		case strings.HasPrefix(path, "/Volumes/") && stat.Flags&unix.MNT_DONTBROWSE == 0:
			mounts = append(mounts, mount{path: path, removable: true})
		}
	}
	return mounts, nil
}

// systemName is the name Finder shows for the system volume: the /Volumes
// entry that links back to /.
func systemName(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "This computer"
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err == nil && target == "/" {
			return entry.Name()
		}
	}
	return "This computer"
}
