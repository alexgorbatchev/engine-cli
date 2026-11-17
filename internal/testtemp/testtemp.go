// Package testtemp prepares the temporary directory of a test process.
package testtemp

import (
	"fmt"
	"os"
	"path/filepath"
)

// UsePhysical points TMPDIR at the physical path of the temporary directory, so
// t.TempDir() returns paths without symbolic links. Engine CLI resolves library
// and media paths to their physical form, and tests compare those with paths
// they build from t.TempDir(). On macOS the default temporary directory sits
// behind the /var symbolic link. Windows reads TMP and TEMP instead, so nothing
// changes there.
func UsePhysical() error {
	physical, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve the temporary directory: %w", err)
	}
	return os.Setenv("TMPDIR", physical)
}
