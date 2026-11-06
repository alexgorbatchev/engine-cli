package intake

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func (s *Store) WatchAdd(ctx context.Context, opts WatchAddOptions) (state WatchState, err error) {
	dir, err := watchfs.Open(opts.Path)
	if err != nil {
		return state, watchFailure("watch_root_invalid", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	if err := s.checkWatchPath(dir.Path()); err != nil {
		return state, err
	}
	identity, err := dir.Identity()
	if err != nil {
		return state, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return state, err
	}
	return s.changeWatch(ctx, opts.Revision, func(ctx context.Context, tx *sql.Tx) error {
		if err := dir.Check(); err != nil {
			return watchFailure("watch_root_changed", err)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM WatchRoot WHERE identity=? AND retired=0)", identity).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return watchFailure("watch_root_exists", errors.New("native watched root is already configured"))
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO WatchRoot(id,path,identity,recursive,enabled) VALUES(?,?,?,?,1)`,
			hex.EncodeToString(nonce[:]), dir.Path(), identity, opts.Recursive)
		return err
	})
}

// WatchSetEnabled changes authorization only. The Service joins an active scan
// before returning a disable/remove response; queue entries remain independent.
func (s *Store) WatchSetEnabled(ctx context.Context, opts WatchRootOptions, enabled bool) (WatchState, error) {
	return s.changeWatch(ctx, opts.Revision, func(ctx context.Context, tx *sql.Tx) error {
		root, identity, err := configuredRoot(ctx, tx, opts.ID)
		if err != nil {
			return err
		}
		if enabled {
			if err := s.checkPinnedRoot(root.Path, identity); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE WatchRoot SET enabled=?,phase='idle',error_code='' WHERE id=?", enabled, opts.ID)
		return err
	})
}

func (s *Store) WatchRemove(ctx context.Context, opts WatchRootOptions) (WatchState, error) {
	return s.changeWatch(ctx, opts.Revision, func(ctx context.Context, tx *sql.Tx) error {
		if _, _, err := configuredRoot(ctx, tx, opts.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE WatchRoot SET retired=1,enabled=0,phase='idle',error_code='' WHERE id=?", opts.ID)
		return err
	})
}

func (s *Store) WatchActivate(ctx context.Context, revision int64) (WatchState, error) {
	return s.changeWatch(ctx, revision, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT path,identity FROM WatchRoot WHERE enabled=1 AND retired=0 ORDER BY sequence")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var path, identity string
			if err := rows.Scan(&path, &identity); err != nil {
				return err
			}
			if err := s.checkPinnedRoot(path, identity); err != nil {
				return err
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE WatchControl SET reason='' WHERE id=1")
		return err
	})
}

func configuredRoot(ctx context.Context, tx *sql.Tx, id string) (WatchRoot, string, error) {
	var root WatchRoot
	var identity string
	err := tx.QueryRowContext(ctx, "SELECT id,path,identity,recursive,enabled FROM WatchRoot WHERE id=? AND retired=0", id).
		Scan(&root.ID, &root.Path, &identity, &root.Recursive, &root.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		err = watchFailure("watch_revision_changed", errors.New("configured watched root no longer exists"))
	}
	return root, identity, err
}

func (s *Store) checkPinnedRoot(path, identity string) (err error) {
	dir, err := watchfs.Open(path)
	if err != nil {
		return watchFailure("watch_root_unavailable", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	current, err := dir.Identity()
	if err != nil {
		return watchFailure("watch_root_unavailable", err)
	}
	if current != identity || dir.Path() != path {
		return watchFailure("watch_root_changed", errors.New("watched root no longer identifies its authorized native directory"))
	}
	return s.checkWatchPath(path)
}

func (s *Store) checkWatchPath(path string) error {
	library, err := os.Stat(s.library)
	if err != nil {
		return err
	}
	// Lexical spellings do not establish containment on case-insensitive or
	// aliased native filesystems. Compare every existing directory ancestor.
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if os.SameFile(library, info) {
				return watchFailure("watch_root_excluded", errors.New("watched sources cannot be inside the target library"))
			}
			parent := filepath.Dir(current)
			for _, name := range []string{".engine-cli", ".engine-cli-intake"} {
				operational, err := os.Stat(filepath.Join(parent, name))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err == nil && os.SameFile(operational, info) {
					return watchFailure("watch_root_excluded", errors.New("watched sources cannot be inside operational storage"))
				}
			}
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func insidePath(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
