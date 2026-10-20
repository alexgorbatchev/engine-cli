package discovery

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func defaultLocations() ([]location, []Warning) { return darwinLocations(unix.Getfsstat) }

func darwinLocations(read func([]unix.Statfs_t, int) (int, error)) ([]location, []Warning) {
	roots, warnings := homeLocations()
	volumes, err := mountedVolumes(read)
	if err != nil {
		warnings = append(warnings, Warning{Message: "listing mounted drives: " + err.Error()})
	}
	for _, volume := range volumes {
		roots = append(roots, driveLocation(volume))
	}
	return roots, warnings
}

func mountedVolumes(read func([]unix.Statfs_t, int) (int, error)) ([]string, error) {
	n, err := read(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	// Leave room for volumes attached between the sizing call and the read.
	mounts := make([]unix.Statfs_t, n+16)
	n, err = read(mounts, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	if n >= len(mounts) {
		return nil, fmt.Errorf("mounted drive list changed; run discovery again")
	}
	var paths []string
	for _, mount := range mounts[:min(n, len(mounts))] {
		if mount.Flags&unix.MNT_LOCAL != 0 {
			paths = append(paths, unix.ByteSliceToString(mount.Mntonname[:]))
		}
	}
	return paths, nil
}
