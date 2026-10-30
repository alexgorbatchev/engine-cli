//go:build unix

package regularfile

import (
	"os"

	"golang.org/x/sys/unix"
)

// O_NONBLOCK prevents FIFO rendezvous before fstat; O_NOCTTY prevents tty capture.
const openFlags = unix.O_NONBLOCK | unix.O_NOCTTY

func blocking(file *os.File) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var reset error
	if err := conn.Control(func(fd uintptr) {
		reset = unix.SetNonblock(int(fd), false)
	}); err != nil {
		return err
	}
	return reset
}
