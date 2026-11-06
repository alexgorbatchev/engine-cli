package intake

import (
	"context"
	"errors"
	"os"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

type preparationSource struct {
	local     staging.LocalFile
	close     func() error
	authorize func(context.Context) error
}

func itemSource(item *Item) staging.LocalSource {
	return staging.LocalSource{Path: item.SourcePath, Identity: item.SourceIdentity, Size: item.SourceSize, Modified: item.SourceModified}
}

// selectPreparationSource runs without staging ownership. The worker retains
// the selected descriptor until staging has released ownership again.
func (s *Store) selectPreparationSource(ctx context.Context, item *Item) (selected *preparationSource, err error) {
	err = intakestate.With(ctx, s.library, func(ctx context.Context) (err error) {
		if err := s.checkPreparation(ctx, item); err != nil {
			return err
		}
		source := itemSource(item)
		file, originalErr := regularfile.Open(source.Path)
		if originalErr == nil {
			originalErr = checkPreparationVersion(file, source)
			if originalErr == nil {
				selected = &preparationSource{local: staging.LocalFile{File: file, Check: func() (err error) {
					current, err := regularfile.Open(source.Path)
					if err != nil {
						return err
					}
					return errors.Join(checkPreparationVersion(current, source), current.Close())
				}}, close: file.Close}
				return nil
			}
			if err := file.Close(); err != nil {
				return errors.Join(originalErr, err)
			}
		}
		path, err := companion.Database(s.library, companion.WatchName)
		if err != nil {
			return err
		}
		if path == "" {
			return originalErr
		}
		d, err := db.Open(path, true)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		state, err := s.watchListHeld(ctx)
		if err != nil {
			return err
		}
		if state.Suspended {
			return watchFailure("watch_suspended", errors.New("restored alias authorization requires activation"))
		}
		rows, err := d.QueryContext(ctx, `SELECT r.path,r.identity,a.path FROM WatchHandoff h
 JOIN WatchVersion v ON v.id=h.version_id JOIN WatchAlias a ON a.version_id=v.id
 JOIN WatchRoot r ON r.id=a.root_id
 WHERE h.item_id=? AND h.source_path=? AND h.source_identity=? AND h.source_size=? AND h.source_modified=?
 AND v.identity=? AND v.size=? AND v.modified=? AND r.enabled=1 AND r.retired=0
 ORDER BY r.sequence,a.path`, item.ID, source.Path, source.Identity, source.Size, source.Modified, source.Identity, source.Size, source.Modified)
		if err != nil {
			return err
		}
		type alias struct{ root, identity, path string }
		var aliases []alias
		for rows.Next() {
			var candidate alias
			if err := rows.Scan(&candidate.root, &candidate.identity, &candidate.path); err != nil {
				return errors.Join(err, rows.Close())
			}
			aliases = append(aliases, candidate)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, candidate := range aliases {
			retained, err := openWatchedSource(candidate.root, candidate.identity, candidate.path)
			if err != nil {
				continue
			}
			if retained.source.Identity != source.Identity || retained.source.Size != source.Size || retained.source.Modified != source.Modified {
				if err := retained.Close(); err != nil {
					return err
				}
				continue
			}
			selected = &preparationSource{local: staging.LocalFile{File: retained.file, Check: retained.Check}, close: retained.Close,
				authorize: func(ctx context.Context) (err error) {
					state, err := s.watchListHeld(ctx)
					if err != nil {
						return err
					}
					if state.Suspended {
						return watchFailure("watch_suspended", errors.New("restored alias authorization requires activation"))
					}
					d, err := db.Open(path, true)
					if err != nil {
						return err
					}
					defer func() { err = errors.Join(err, d.Close()) }()
					var allowed bool
					err = d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM WatchAlias a
 JOIN WatchRoot r ON r.id=a.root_id JOIN WatchVersion v ON v.id=a.version_id
 JOIN WatchHandoff h ON h.version_id=v.id JOIN WatchControl c ON c.id=1
 WHERE a.path=? AND r.path=? AND r.identity=? AND r.enabled=1 AND r.retired=0 AND c.reason=''
 AND h.item_id=? AND h.source_path=? AND h.source_identity=? AND h.source_size=? AND h.source_modified=?
 AND v.identity=? AND v.size=? AND v.modified=?)`, candidate.path, candidate.root, candidate.identity,
						item.ID, source.Path, source.Identity, source.Size, source.Modified, source.Identity, source.Size, source.Modified).Scan(&allowed)
					if err != nil {
						return err
					}
					if !allowed {
						return watchFailure("watch_root_changed", errors.New("selected alias authorization was revoked"))
					}
					return retained.Check()
				}}
			return nil
		}
		return originalErr
	})
	if err != nil && selected != nil {
		err = errors.Join(err, selected.close())
		selected = nil
	}
	return selected, err
}

// checkPreparation requires the state owner and never acquires the worker.
func (s *Store) checkPreparation(ctx context.Context, item *Item) error {
	if err := s.requireActive(ctx); err != nil {
		return err
	}
	current, err := scanItem(s.database.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM Item WHERE id=?", item.ID))
	if err != nil {
		return err
	}
	if !watchItemMatches(current, itemSource(item)) || current.Status != "preparing" || current.Attempt != item.Attempt || current.StageID != item.StageID || current.CancelRequested || current.HistoryID != "" {
		return stateChanged()
	}
	return nil
}

func checkPreparationVersion(file *os.File, source staging.LocalSource) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	identity, err := fileidentity.IdentityFile(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || identity != source.Identity || info.Size() != source.Size || info.ModTime().UnixNano() != source.Modified {
		return watchFailure("intake_source_changed", errors.New("selected source differs from the captured queue version"))
	}
	return nil
}
