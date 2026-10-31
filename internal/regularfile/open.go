// Package regularfile opens retained regular-file descriptors for media readers.
package regularfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Open rejects special files without waiting for a FIFO peer on Unix.
func Open(path string) (*os.File, error) {
	return accept(os.OpenFile(path, os.O_RDONLY|openFlags, 0))
}

// OpenRoot preserves the root's native path confinement and descriptor ownership.
func OpenRoot(root *os.Root, name string) (*os.File, error) {
	return accept(root.OpenFile(name, os.O_RDONLY|openFlags, 0))
}

func accept(file *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("open regular file %q: %w", file.Name(), fs.ErrInvalid)
	}
	if err == nil {
		err = blocking(file)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
