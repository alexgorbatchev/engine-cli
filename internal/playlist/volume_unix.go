//go:build !windows

package playlist

import (
	"os"
	"syscall"
)

// sameVolume reports whether two existing paths sit on one device.
func sameVolume(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	if err != nil {
		return false
	}
	l, lok := left.Sys().(*syscall.Stat_t)
	r, rok := right.Sys().(*syscall.Stat_t)
	return lok && rok && l.Dev == r.Dev
}
