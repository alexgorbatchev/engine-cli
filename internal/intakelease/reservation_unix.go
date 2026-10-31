//go:build linux || darwin

package intakelease

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockReservation(file *os.File, exclusive bool) error {
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	return unix.Flock(int(file.Fd()), mode|unix.LOCK_NB)
}

func nativeReservationBusy(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
