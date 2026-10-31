package intake

import (
	"context"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

// PrepareNext processes one FIFO item under native worker ownership. Quiet time
// is an observation heuristic, not a promise that another process has finished
// writing. Native identity/size/mtime are checked around the retained-file copy.
func (s *Store) PrepareNext(ctx context.Context, quiet time.Duration) (*Item, error) {
	var result *Item
	err := s.withWorker(ctx, func(ctx context.Context) error {
		state, err := intakecontrol.Read(ctx, s.database)
		if err != nil || state.Paused {
			return err
		}
		// Only the worker owner may resume abandoned preparation. Import intent
		// is deliberately excluded: its collection/history receipt needs a
		// separate reconciliation before any new import can be attempted.
		if _, err := s.database.ExecContext(ctx, `UPDATE Item SET
 status=CASE WHEN cancel_requested=1 THEN 'cancelled' ELSE 'queued' END
 WHERE status='preparing'`); err != nil {
			return err
		}
		item, err := s.Claim(ctx)
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
		if current.CancelRequested {
			completion.ErrorCode = "cancelled"
		} else {
			copy, copyErr := staging.New(s.library).CopyLocal(ctx, item.StageID, staging.LocalSource{
				Path: item.SourcePath, Identity: item.SourceIdentity, Size: item.SourceSize, Modified: item.SourceModified,
			})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if copyErr != nil {
				completion.ErrorCode = progress.ErrorCode(copyErr)
			} else {
				completion.StageID, completion.ContentHash = copy.Item.ID, copy.ContentHash
			}
		}
		result, err = s.CompleteStage(ctx, completion)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
