//go:build darwin || linux

package transfer

import "golang.org/x/sys/unix"

func availableSpace(path string) (uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return 0, err
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}
