package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"zombiezen.com/go/sqlite"
)

func openHistoryLock(ctx context.Context, dir string, flags sqlite.OpenFlags, wait bool) (*sqlite.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "lock.db")
	if err := validateHistoryLock(path); err != nil && !(flags&sqlite.OpenCreate != 0 && errors.Is(err, os.ErrNotExist)) {
		return nil, err
	}
	c, err := open(ctx, path, flags)
	if err != nil {
		return nil, err
	}
	if !wait {
		c.SetBusyTimeout(0)
	}
	return c, nil
}

// Owners only hold transactions in this dedicated database; they never write
// SQL data, so it remains empty. Reject initialized databases before SQLite
// can create WAL sidecars. Stat does not open or close a separate descriptor,
// which would silently release this process's SQLite locks on POSIX systems.
func validateHistoryLock(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return fmt.Errorf("history lock database must be an empty regular file")
	}
	return nil
}

func checkHistoryJournal(c *sqlite.Conn) error {
	modes, err := queryStrings(c, "PRAGMA journal_mode")
	if err != nil {
		return err
	}
	if len(modes) == 1 {
		switch modes[0] {
		case "delete", "truncate", "persist":
			return nil
		}
	}
	return fmt.Errorf("history lock database requires a rollback journal: %v", modes)
}
