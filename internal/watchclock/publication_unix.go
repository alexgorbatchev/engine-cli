//go:build linux || darwin

package watchclock

import (
	"errors"
	"os"
	"path/filepath"
)

func publish(source, destination string) error {
	if err := renameExclusive(source, destination); err != nil {
		return &os.LinkError{Op: "publish watch authority", Old: source, New: destination, Err: err}
	}
	d, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
