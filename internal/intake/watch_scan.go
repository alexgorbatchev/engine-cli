package intake

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

type watchDirectory struct{ path, identity string }

type watchObservation struct {
	source  staging.LocalSource
	version int64
}

// scanWatchRoot collects fresh native observations. Nothing is published from
// an incomplete traversal; all descendant opens refuse links and devices.
func (s *Store) scanWatchRoot(ctx context.Context, root WatchRoot, identity string) (sources []staging.LocalSource, directories []watchDirectory, err error) {
	dir, err := watchfs.Open(root.Path)
	if err != nil {
		return nil, nil, watchFailure("watch_root_unavailable", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	current, err := dir.Identity()
	if err != nil || current != identity || dir.Path() != root.Path {
		return nil, nil, watchFailure("watch_root_changed", errors.Join(err, errors.New("configured native directory changed")))
	}
	visited := make(map[string]bool)
	var walk func(*watchfs.Directory) error
	walk = func(dir *watchfs.Directory) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := dir.Identity()
		if err != nil {
			return err
		}
		if visited[id] {
			return nil
		}
		visited[id] = true
		directories = append(directories, watchDirectory{path: dir.Path(), identity: id})
		entries, err := dir.Entries()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := filepath.Join(dir.Path(), entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if err := s.checkWatchPath(path); err != nil {
				if progress.ErrorCode(err) == "watch_root_excluded" || errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			if entry.IsDir() {
				if !root.Recursive {
					continue
				}
				child, err := dir.OpenDirectory(entry.Name())
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if err := errors.Join(walk(child), child.Close()); err != nil {
					return err
				}
				continue
			}
			if !entry.Type().IsRegular() || !watchAudioFile(entry.Name()) {
				continue
			}
			selected, err := openWatchedSource(root.Path, identity, path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			source := selected.source
			if err := errors.Join(selected.Check(), selected.Close()); err != nil {
				return err
			}
			sources = append(sources, source)
		}
		return dir.Check()
	}
	err = walk(dir)
	return sources, directories, err
}

// Extensions select candidates, not trusted media. Preparation still validates
// copied bytes using the normal native metadata/audio tools.
func watchAudioFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav", ".wave", ".aif", ".aiff", ".flac", ".alac", ".mp3", ".m4a", ".aac", ".mp4", ".ogg", ".opus":
		return true
	}
	return false
}

func (s *Store) observeWatchRoot(ctx context.Context, root WatchRoot, revision int64, sources []staging.LocalSource) (observations []watchObservation, err error) {
	err = intakestate.With(ctx, s.library, func(ctx context.Context) (err error) {
		state, err := s.watchListHeld(ctx)
		if err != nil {
			return err
		}
		if state.Revision != revision || state.Suspended {
			return watchFailure("watch_revision_changed", errors.New("watch configuration changed during traversal"))
		}
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		current, _, err := configuredRoot(ctx, tx, root.ID)
		if err != nil {
			return err
		}
		if !current.Enabled {
			return watchFailure("watch_revision_changed", errors.New("watch authorization revoked during traversal"))
		}
		// Preserve quiet observations across aliases and overlapping roots, but
		// require a new quiet interval after a version has disappeared entirely.
		rows, err := tx.QueryContext(ctx, "SELECT DISTINCT version_id FROM WatchAlias")
		if err != nil {
			return err
		}
		continuous := make(map[int64]bool)
		for rows.Next() {
			var version int64
			if err := rows.Scan(&version); err != nil {
				return errors.Join(err, rows.Close())
			}
			continuous[version] = true
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM WatchAlias WHERE root_id=?", root.ID); err != nil {
			return err
		}
		now := time.Now().UnixNano()
		for _, source := range sources {
			var version int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO WatchVersion(identity,size,modified,stable_since) VALUES(?,?,?,?)
 ON CONFLICT(identity,size,modified) DO UPDATE SET identity=excluded.identity RETURNING id`,
				source.Identity, source.Size, source.Modified, now).Scan(&version); err != nil {
				return err
			}
			if !continuous[version] {
				if _, err := tx.ExecContext(ctx, "UPDATE WatchVersion SET stable_since=? WHERE id=?", now, version); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO WatchAlias(version_id,root_id,path,seen_at) VALUES(?,?,?,?)", version, root.ID, source.Path, now); err != nil {
				return err
			}
			observations = append(observations, watchObservation{source: source, version: version})
		}
		_, err = tx.ExecContext(ctx, "UPDATE WatchRoot SET phase='idle',last_scan_at=?,error_code='',observed=? WHERE id=?", now, len(sources), root.ID)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	return observations, err
}

func (s *Store) pinnedWatchRoot(ctx context.Context, id string) (root WatchRoot, identity string, err error) {
	d, err := openWatch(ctx, s.library)
	if err != nil {
		return root, identity, err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return root, identity, err
	}
	defer tx.Rollback()
	root, identity, err = configuredRoot(ctx, tx, id)
	if err != nil {
		return root, identity, err
	}
	return root, identity, tx.Commit()
}
