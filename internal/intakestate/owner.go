// Package intakestate coordinates durable queue/discovery transitions and paired
// companion captures without retaining a transaction on either data database.
package intakestate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const ownerName = "intake-state.db"

// With retains one cancellable native process owner until run returns. Callers
// acquire History and preparation ownership before this gate, never inside run.
// Callbacks must not call another public operation that reacquires this gate.
func With(ctx context.Context, library string, run func(context.Context) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(library, ".engine-cli")
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	path := filepath.Join(dir, ownerName)
	if err := checkStorage(dir, path); err != nil {
		return err
	}
	owner, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	owner.SetInterrupt(ctx.Done())
	owner.SetBlockOnBusy()
	if err := rollbackMode(owner); err != nil {
		return errors.Join(ctx.Err(), err)
	}
	if err := sqlitex.ExecuteTransient(owner, "BEGIN EXCLUSIVE", nil); err != nil {
		return errors.Join(ctx.Err(), err)
	}
	if err := rollbackMode(owner); err != nil {
		return errors.Join(ctx.Err(), err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A cancelled callback must join its work before releasing exclusion; its
	// interrupt channel must not drop ownership underneath paired publication.
	owner.SetInterrupt(nil)
	return run(ctx)
}

// Observe retains shared native exclusion without creating storage. It calls
// run only when an existing state owner was observed. Writers hold EXCLUSIVE
// exclusion across their independent companion commits, so the callback sees
// one coherent authority/configuration boundary.
func Observe(ctx context.Context, library string, run func(context.Context) error) (present bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dir := filepath.Join(library, ".engine-cli")
	path := filepath.Join(dir, ownerName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := checkStorage(dir, path); err != nil {
		return false, err
	}
	owner, err := sqlite.OpenConn(path, sqlite.OpenReadOnly)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	owner.SetInterrupt(ctx.Done())
	owner.SetBlockOnBusy()
	if err := rollbackMode(owner); err != nil {
		return true, errors.Join(ctx.Err(), err)
	}
	if err := sqlitex.ExecuteTransient(owner, "BEGIN", nil); err != nil {
		return true, errors.Join(ctx.Err(), err)
	}
	// BEGIN alone is deferred. Reading the schema acquires the actual SHARED
	// native lock even when the operational database contains no application rows.
	if err := sqlitex.ExecuteTransient(owner, "SELECT count(*) FROM sqlite_schema", nil); err != nil {
		return true, errors.Join(ctx.Err(), err)
	}
	if err := rollbackMode(owner); err != nil {
		return true, errors.Join(ctx.Err(), err)
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	owner.SetInterrupt(nil)
	return true, run(ctx)
}

func rollbackMode(owner *sqlite.Conn) error {
	var mode string
	if err := sqlitex.ExecuteTransient(owner, "PRAGMA journal_mode", &sqlitex.ExecOptions{ResultFunc: func(statement *sqlite.Stmt) error {
		mode = statement.ColumnText(0)
		return nil
	}}); err != nil {
		return err
	}
	if mode != "delete" {
		return fmt.Errorf("intake state exclusion requires DELETE journal mode, found %q", mode)
	}
	return nil
}

func checkStorage(dir, path string) error {
	for _, candidate := range []string{dir, path} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) && candidate == path {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || candidate == dir && !info.IsDir() || candidate == path && !info.Mode().IsRegular() {
			return fmt.Errorf("intake state owner requires regular storage: %w", fs.ErrInvalid)
		}
	}
	return nil
}
