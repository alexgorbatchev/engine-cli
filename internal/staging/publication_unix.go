//go:build !windows

package staging

import (
	"errors"
	"os"
	"path/filepath"
)

func publishManifest(root *os.Root, id, tmp string) error {
	if err := root.Rename(tmp, filepath.Join(id, manifestName)); err != nil {
		return err
	}
	dir, err := root.Open(id)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
