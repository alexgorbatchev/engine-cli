package companion

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// OpenWrite exclusively enrolls absent storage. The caller initializes its
// schema transactionally and removes a newly enrolled file if publication fails.
func OpenWrite(ctx context.Context, library, name string) (*sql.DB, bool, error) {
	existing, err := Database(library, name)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	path := filepath.Join(library, ".engine-cli", name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	created := existing == ""
	if created {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, false, err
		}
		if err := file.Close(); err != nil {
			return nil, false, errors.Join(err, Remove(path))
		}
	}
	d, err := db.Open(path)
	if err != nil {
		if created {
			err = errors.Join(err, Remove(path))
		}
		return nil, false, err
	}
	return d, created, nil
}

// Remove is used only after closing every owned database handle, under the
// collection writer/history boundary which owns the companion's before-image.
func Remove(path string) error {
	var result error
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}
