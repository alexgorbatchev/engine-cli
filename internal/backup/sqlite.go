package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
)

// The driver's connection type is private; Conn.Raw exposes its documented
// online backup method through this interface.
type sqliteBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func databaseURI(path, mode string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"mode": {mode}}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// snapshotDatabase copies SQLite's committed view, including WAL pages, without
// checkpointing or copying the live database's journal files.
func snapshotDatabase(ctx context.Context, src, dst string) (size int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	sourceURI, err := databaseURI(src, "ro")
	if err != nil {
		return 0, err
	}
	destinationURI, err := databaseURI(dst, "rw")
	if err != nil {
		return 0, err
	}
	database, err := sql.Open("sqlite", sourceURI)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	conn, err := database.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("opening source database: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	// Reserve exclusively so an existing snapshot can never be overwritten.
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				_ = os.Remove(dst + suffix)
			} // Best-effort cleanup of this reserved destination.
		}
	}()
	if err := file.Close(); err != nil {
		return 0, err
	}
	err = conn.Raw(func(raw any) (err error) {
		driver, ok := raw.(sqliteBackuper)
		if !ok {
			return fmt.Errorf("SQLite driver does not expose the online backup API")
		}
		operation, err := driver.NewBackup(destinationURI)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, operation.Finish()) }()
		const pagesPerStep = 256
		for more := true; more; {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err = operation.Step(pagesPerStep)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("copying SQLite snapshot: %w", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
