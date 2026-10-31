package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ErrHistoryAbsent identifies missing ownership storage before an observation
// callback runs. Callback errors never acquire this classification.
var ErrHistoryAbsent = errors.New("History ownership storage is absent")

// ObserveEntries waits for existing History ownership and retains it through
// observe. It never creates History storage. Callers must acquire their other
// owners inside observe, and must not acquire History again from that callback.
// Cancellation interrupts native acquisition; List retains its fail-fast policy.
func ObserveEntries(ctx context.Context, lib string, observe func(context.Context, []Entry) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := storeDir(lib)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "lock.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %w", ErrHistoryAbsent, err)
		}
		return err
	}
	owner, err := open(ctx, path, sqlite.OpenReadWrite)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	if err := sqlitex.ExecuteTransient(owner, "BEGIN IMMEDIATE", nil); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if err := checkSmartlistRepair(dir); err != nil {
		return err
	}
	observed, err := entries(dir)
	if err != nil {
		return err
	}
	return observe(ctx, observed)
}
