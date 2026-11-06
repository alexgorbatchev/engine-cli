package watchclock

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func publish(source, destination string) error {
	from, err := nativePath(source)
	if err != nil {
		return err
	}
	to, err := nativePath(destination)
	if err != nil {
		return err
	}
	// Without REPLACE_EXISTING an existing authority is never overwritten.
	// WRITE_THROUGH is the native publication flush; read-only Windows directory
	// handles cannot be flushed like their Unix counterparts.
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "publish watch authority", Old: source, New: destination, Err: err}
	}
	return nil
}

func nativePath(path string) (*uint16, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(path, `\\?\`), strings.HasPrefix(path, `\\.\`):
	case strings.HasPrefix(path, `\\`):
		path = `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	default:
		path = `\\?\` + path
	}
	return windows.UTF16PtrFromString(path)
}
