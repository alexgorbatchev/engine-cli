//go:build !windows

package history

import (
	"errors"
	"os"
	"path/filepath"
)

func publishEntry(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
