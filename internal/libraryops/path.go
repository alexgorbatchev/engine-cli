package libraryops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Path validates an existing collection using a native read-only SQLite URI.
func Path(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("library path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Join(abs, "Database2", "m.db"))
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("library database must be a regular file")
	}
	d, err := collection(ctx, abs, true)
	if err != nil {
		return "", err
	}
	_, err = d.ExecContext(ctx, "SELECT id FROM Track LIMIT 0; SELECT id FROM Playlist LIMIT 0")
	if err = errors.Join(err, d.Close()); err != nil {
		return "", err
	}
	return abs, nil
}
