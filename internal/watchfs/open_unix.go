//go:build linux || darwin

package watchfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func nativeRoot(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
}

func nativeChild(parent *os.File, name string, directory bool) (*os.File, error) {
	conn, err := parent.SyscallConn()
	if err != nil {
		return nil, err
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd := -1
	var openErr error
	if err := conn.Control(func(parentFD uintptr) {
		fd, openErr = unix.Openat(int(parentFD), name, flags, 0)
	}); err != nil {
		return nil, err
	}
	path := filepath.Join(parent.Name(), name)
	if openErr != nil {
		return nil, &os.PathError{Op: "openat watched child", Path: path, Err: openErr}
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && (directory && !info.IsDir() || !directory && !info.Mode().IsRegular()) {
		err = fs.ErrInvalid
	}
	if err == nil && !directory {
		err = unix.SetNonblock(fd, false)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
