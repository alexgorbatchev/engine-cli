package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Native Engine schema 3.0.2, also exercised by the transfer integration fixtures.
//
//go:embed library_schema.sql
var librarySchema string

// CreateLibrary initializes a new Database2/m.db beneath an existing directory.
// Existing Database2 directories are never opened for writing or replaced.
// Callers apply desktop protection and operation history before calling it.
func CreateLibrary(ctx context.Context, root string) (id string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("library directory must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := filepath.Join(root, "Database2")
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		for _, name := range []string{"m.db-journal", "m.db-wal", "m.db-shm", "m.db"} {
			if cleanupErr := os.Remove(filepath.Join(dir, name)); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				err = errors.Join(err, cleanupErr)
			}
		}
		err = errors.Join(err, os.Remove(dir))
	}()
	d, err := Open(filepath.Join(dir, "m.db"))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if _, err = tx.ExecContext(ctx, librarySchema); err != nil {
		return "", fmt.Errorf("create Engine schema: %w", err)
	}
	identity, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	id = identity.String()
	if _, err = tx.ExecContext(ctx, "INSERT INTO Information(id,uuid,schemaVersionMajor,schemaVersionMinor,schemaVersionPatch,currentPlayedIndiciator,lastRekordBoxLibraryImportReadCounter) VALUES(1,?,3,0,2,0,0)", id); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	complete = true
	return id, nil
}
