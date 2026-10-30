// Package companion identifies CLI-owned databases enrolled in library recovery.
package companion

import (
	"fmt"
	"os"
	"path/filepath"
)

const TagsName = "tags.db"

func TagsPath(library string) string {
	return filepath.Join(library, ".engine-cli", TagsName)
}

// Databases lists only explicitly enrolled databases. Recovery directories and
// other CLI state must never become recursive snapshot inputs.
func Databases(library string) ([]string, error) {
	path := TagsPath(library)
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("CLI companion database paths must not be symbolic links")
		}
		if candidate == path && !info.Mode().IsRegular() || candidate != path && !info.IsDir() {
			return nil, fmt.Errorf("CLI companion storage requires a regular database in a directory")
		}
	}
	return []string{path}, nil
}
