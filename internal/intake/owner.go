package intake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// withWorker retains a native process owner in separate operational storage.
// Queue writes remain available while media preparation runs. Closing or losing
// the process releases the SQLite transaction; the lock is never snapshotted.
func (s *Store) withWorker(ctx context.Context, run func(context.Context) error) (err error) {
	path := filepath.Join(s.library, ".engine-cli", "intake-owner.db")
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) && candidate == path {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || candidate == path && !info.Mode().IsRegular() || candidate != path && !info.IsDir() {
			return fmt.Errorf("intake owner requires regular storage in a directory")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	owner, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	owner.SetInterrupt(ctx.Done())
	owner.SetBusyTimeout(0)
	if err := sqlitex.ExecuteTransient(owner, "BEGIN IMMEDIATE", nil); err != nil {
		return &progress.Error{Code: "intake_worker_busy", Cause: err}
	}
	// Cancellation stops the worker callback, not its ownership. Retain exclusion
	// until the callback has joined every in-flight preparation and returned.
	owner.SetInterrupt(nil)
	return run(ctx)
}
