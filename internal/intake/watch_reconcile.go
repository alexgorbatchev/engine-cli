package intake

import (
	"context"
	"errors"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// reconcileWatch runs under discovery ownership, never preparation ownership.
// Every publication rechecks durable authorization and queue suspension.
func (s *Store) reconcileWatch(ctx context.Context, quiet time.Duration) (scan WatchScan, directories []watchDirectory, err error) {
	state, err := s.WatchList(ctx)
	if err != nil {
		return scan, nil, err
	}
	scan.State = state
	if state.Suspended {
		return scan, nil, watchFailure("watch_suspended", errors.New("restored watch configuration requires activation"))
	}
	var failures error
	for _, root := range state.Roots {
		if !root.Enabled {
			continue
		}
		if err := ctx.Err(); err != nil {
			return scan, directories, err
		}
		scan.Roots++
		pinned, identity, scanErr := s.pinnedWatchRoot(ctx, root.ID)
		var observations []watchObservation
		if scanErr == nil {
			observations, scanErr = s.reconcileWatchRoot(ctx, pinned, identity, state.Revision, &directories)
		}
		if scanErr != nil {
			failures = errors.Join(failures, scanErr, s.watchRootFailed(ctx, root.ID, state.Revision, scanErr))
			continue
		}
		scan.Observed += int64(len(observations))
		for _, observation := range observations {
			selected, openErr := openWatchedSource(pinned.Path, identity, observation.source.Path)
			if openErr != nil {
				failures = errors.Join(failures, openErr)
				continue
			}
			item, provenance, published, publishErr := s.publishWatchHandoff(ctx, observation.version, observation.source, quiet, selected.Check)
			publishErr = errors.Join(publishErr, selected.Close())
			if publishErr != nil {
				failures = errors.Join(failures, publishErr)
				continue
			}
			if item == nil {
				scan.Pending++
			} else if published && provenance == "created" {
				scan.Queued++
			} else if published {
				scan.Associated++
			}
		}
	}
	scan.State, err = s.WatchList(ctx)
	err = errors.Join(failures, err)
	scan.Completed = err == nil
	return scan, directories, err
}

func (s *Store) reconcileWatchRoot(ctx context.Context, root WatchRoot, identity string, revision int64, directories *[]watchDirectory) ([]watchObservation, error) {
	sources, dirs, err := s.scanWatchRoot(ctx, root, identity)
	if err != nil {
		return nil, err
	}
	observations, err := s.observeWatchRoot(ctx, root, revision, sources)
	if err == nil {
		*directories = append(*directories, dirs...)
	}
	return observations, err
}

func (s *Store) watchRootFailed(ctx context.Context, id string, revision int64, cause error) error {
	return intakestate.With(ctx, s.library, func(ctx context.Context) (err error) {
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		_, err = d.ExecContext(ctx, `UPDATE WatchRoot SET phase='degraded',error_code=?
 WHERE id=? AND retired=0 AND enabled=1 AND (SELECT revision FROM WatchControl WHERE id=1)=?`, progress.ErrorCode(cause), id, revision)
		return err
	})
}
