// Package watchclock retains authorization generations outside portable images.
// Callers hold the intake state owner while observing or allocating a revision.
package watchclock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const filename = "watch-authority.db"
const applicationID = 0x454E4741
const schemaVersion = 1

type State struct {
	Revision int64
	Restored int64
}

// Read never creates storage. An absent authority has generation zero.
func Read(ctx context.Context, library string) (state State, err error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	path, present, err := storage(library)
	if err != nil || !present {
		return state, err
	}
	d, err := db.Open(path, true)
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	return read(ctx, d)
}

// Advance commits a fresh generation before any independently committed image
// mutation. Failed later publication consumes that generation rather than
// allowing an older consent token to become valid again.
func Advance(ctx context.Context, library string, floor int64) (revision int64, err error) {
	return allocate(ctx, library, floor, false)
}

// Restore fences configuration before either portable image is replaced. Even
// a crash before the image's restored marker is written leaves discovery
// suspended: that image's revision is older than this retained watermark.
func Restore(ctx context.Context, library string, floor int64) (int64, error) {
	return allocate(ctx, library, floor, true)
}

func allocate(ctx context.Context, library string, floor int64, restored bool) (revision int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if floor < 0 {
		return 0, errors.New("invalid watch authorization generation")
	}
	path, present, err := storage(library)
	if err != nil {
		return 0, err
	}
	if !present {
		return initialize(ctx, path, floor, restored)
	}
	return allocateFile(ctx, path, floor, restored, false)
}

func allocateFile(ctx context.Context, path string, floor int64, restored, initial bool) (revision int64, err error) {
	d, tx, err := beginAllocation(ctx, path, initial)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	defer tx.Rollback()
	current, err := read(ctx, tx)
	if err != nil {
		return 0, err
	}
	if floor = max(current.Revision, floor); floor == math.MaxInt64 {
		return 0, errors.New("watch authorization generation exhausted")
	}
	revision = floor + 1
	if restored {
		current.Restored = revision
	}
	if _, err := tx.ExecContext(ctx, "UPDATE Authority SET revision=?,restored=? WHERE id=1", revision, current.Restored); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revision, nil
}

func beginAllocation(ctx context.Context, path string, initial bool) (*sql.DB, *sql.Tx, error) {
	d, err := db.Open(path)
	if err != nil {
		return nil, nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, errors.Join(err, d.Close())
	}
	if initial {
		if err := initializeSchema(ctx, tx); err != nil {
			return nil, nil, errors.Join(err, tx.Rollback(), d.Close())
		}
	}
	return d, tx, nil
}

func initializeSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id=%d; PRAGMA user_version=%d;
CREATE TABLE Authority(id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL CHECK(revision>=0),restored INTEGER NOT NULL CHECK(restored>=0 AND restored<=revision)) STRICT;
INSERT INTO Authority VALUES(1,0,0)`, applicationID, schemaVersion))
	return err
}

func initialize(ctx context.Context, path string, floor int64, restored bool) (revision int64, err error) {
	f, err := newInitialization(path)
	if err != nil {
		return 0, err
	}
	temporary := f.Name()
	defer func() { err = errors.Join(err, removeTemporary(temporary)) }()
	if err := f.Close(); err != nil {
		return 0, err
	}
	// All SQLite handles must close before publication so its journal cannot
	// become separated from the database it protects.
	revision, err = allocateFile(ctx, temporary, floor, restored, true)
	if err != nil {
		return 0, err
	}
	f, err = os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// A directory flush can fail after rename succeeded. Never remove the final
	// authority on that error: the published generation remains consumed.
	if err := publish(temporary, path); err != nil {
		return 0, err
	}
	return revision, nil
}

func removeTemporary(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func newInitialization(path string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(path), ".watch-authority-*.db")
}

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func read(ctx context.Context, d reader) (state State, err error) {
	var application, version int
	if err := d.QueryRowContext(ctx, "SELECT application_id,user_version FROM pragma_application_id,pragma_user_version").Scan(&application, &version); err != nil {
		return state, err
	}
	if application != applicationID || version != schemaVersion {
		return state, errors.New("not a supported watch authority database")
	}
	if err := d.QueryRowContext(ctx, "SELECT revision,restored FROM Authority WHERE id=1").Scan(&state.Revision, &state.Restored); err != nil {
		return state, err
	}
	if state.Revision < 0 || state.Restored < 0 || state.Restored > state.Revision {
		return state, errors.New("invalid watch authority generation")
	}
	return state, nil
}

func storage(library string) (path string, present bool, err error) {
	dir := filepath.Join(library, ".engine-cli")
	path = filepath.Join(dir, filename)
	for _, candidate := range []string{dir, path} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return path, false, nil
		}
		if err != nil {
			return path, false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || candidate == dir && !info.IsDir() || candidate == path && !info.Mode().IsRegular() {
			return path, false, errors.New("watch authority requires regular native storage")
		}
	}
	return path, true, nil
}
