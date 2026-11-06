package intake

import (
	"context"
	"errors"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

// PrepareNext processes one FIFO item under native worker ownership. Quiet time
// is an observation heuristic, not a promise that another process has finished
// writing. Native identity/size/mtime are checked around the retained-file copy.
func (s *Store) PrepareNext(ctx context.Context, quiet time.Duration) (*Item, error) {
	var result *Item
	err := s.withWorker(ctx, func(ctx context.Context) (err error) {
		state, err := intakecontrol.Read(ctx, s.database)
		if err != nil || state.Paused {
			return err
		}
		// Only the worker owner may resume abandoned preparation. Import intent
		// is deliberately excluded: its collection/history receipt needs a
		// separate reconciliation before any new import can be attempted.
		item, err := s.change(ctx, func(ctx context.Context) (*Item, error) {
			if _, err := s.database.ExecContext(ctx, `UPDATE Item SET
 status=CASE WHEN cancel_requested=1 THEN 'cancelled' ELSE 'queued' END
 WHERE status='preparing'`); err != nil {
				return nil, err
			}
			return s.claim(ctx)
		})
		if err != nil || item == nil {
			return err
		}
		if quiet > 0 {
			timer := time.NewTimer(quiet)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		current, err := scanItem(s.database.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM Item WHERE id=?", item.ID))
		if err != nil {
			return err
		}
		completion := StageResult{ID: item.ID, Attempt: item.Attempt}
		var selected *preparationSource
		if current.CancelRequested {
			completion.ErrorCode = "cancelled"
		} else {
			copy, copyErr := staging.New(s.library).CopyLocalFile(ctx, item.StageID, itemSource(item), func() (staging.LocalFile, error) {
				var err error
				selected, err = s.selectPreparationSource(ctx, item)
				if err != nil {
					return staging.LocalFile{}, err
				}
				return selected.local, nil
			})
			// Borrowed native ownership survives until staging releases its lock.
			// Keep it through the final state-owned authorization check below.
			if selected != nil {
				defer func() {
					if selected != nil {
						err = errors.Join(err, selected.close())
					}
				}()
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if copyErr != nil {
				completion.ErrorCode = progress.ErrorCode(copyErr)
			} else {
				completion.StageID, completion.ContentHash = copy.Item.ID, copy.ContentHash
			}
		}
		err = intakestate.With(ctx, s.library, func(ctx context.Context) error {
			if selected != nil && selected.authorize != nil && completion.ErrorCode == "" {
				if err := selected.authorize(ctx); err != nil {
					completion.StageID, completion.ContentHash = "", ""
					completion.ErrorCode = progress.ErrorCode(err)
				}
			}
			if selected != nil {
				closeErr := selected.close()
				selected = nil
				if closeErr != nil {
					return closeErr
				}
			}
			result, err = s.completeStage(ctx, completion)
			return err
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
