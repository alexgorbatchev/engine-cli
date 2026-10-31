// Package companion identifies CLI-owned databases enrolled in library recovery.
package companion

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	TagsName    = "tags.db"
	ImportsName = "imports.db"
)

func TagsPath(library string) string {
	return filepath.Join(library, ".engine-cli", TagsName)
}

func ImportsPath(library string) string {
	return filepath.Join(library, ".engine-cli", ImportsName)
}

// Databases lists only explicitly enrolled databases. Recovery directories and
// other CLI state must never become recursive snapshot inputs.
func Databases(library string) ([]string, error) {
	var paths []string
	for _, name := range []string{TagsName, ImportsName} {
		path, err := Database(library, name)
		if err != nil {
			return nil, err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// Database observes one enrolled companion without mistaking another database's
// presence for enrollment. An absent database is returned as an empty path.
func Database(library, name string) (string, error) {
	if name != TagsName && name != ImportsName {
		return "", fmt.Errorf("unknown CLI companion database %q", name)
	}
	path := filepath.Join(library, ".engine-cli", name)
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("CLI companion database paths must not be symbolic links")
		}
		if candidate == path && !info.Mode().IsRegular() || candidate != path && !info.IsDir() {
			return "", fmt.Errorf("CLI companion storage requires a regular database in a directory")
		}
	}
	return path, nil
}
