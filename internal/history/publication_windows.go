package history

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func publishEntry(source, destination string) error {
	from, err := entryPath(source)
	if err != nil {
		return err
	}
	to, err := entryPath(destination)
	if err != nil {
		return err
	}
	// A read-only directory handle cannot be flushed on Windows. Request
	// write-through publication after writeAtomic has flushed the file itself.
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "publish history entry", Old: source, New: destination, Err: err}
	}
	return nil
}

func entryPath(path string) (*uint16, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// MoveFileEx does not perform Go's automatic long-path conversion. Preserve
	// existing device paths and extend ordinary drive/UNC paths explicitly.
	switch {
	case strings.HasPrefix(path, `\\?\`), strings.HasPrefix(path, `\\.\`):
	case strings.HasPrefix(path, `\\`):
		path = `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	default:
		path = `\\?\` + path
	}
	return windows.UTF16PtrFromString(path)
}
