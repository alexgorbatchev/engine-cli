package sync

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic publishes metadata using a unique temporary file in the same directory.
// Metadata avoids a physical Sync so cancellation does not wait for it.
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".metadata-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		f.Close()           // Best-effort cleanup; the successful path checks Close below.
		os.Remove(f.Name()) // Successful rename already removed this name.
	}()
	if err := f.Chmod(0644); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("publishing %q: %w", path, err)
	}
	return nil
}
