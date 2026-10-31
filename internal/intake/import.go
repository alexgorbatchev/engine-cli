package intake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
)

type ImportOptions struct {
	ID         int64
	ToPlaylist string
	PlanToken  string
	Runner     enginesync.CommandRunner
}

type ImportReceipt struct {
	ItemID    int64  `json:"item_id"`
	HistoryID string `json:"history_id"`
	TrackID   int64  `json:"track_id"`
}

func (s *Store) ReviewImport(ctx context.Context, d *sql.DB, opts ImportOptions) (*staging.ImportReview, error) {
	item, err := s.stagedImport(ctx, opts.ID)
	if err != nil {
		return nil, err
	}
	return staging.New(s.library).ReviewImport(ctx, d, staging.ImportOptions{IDs: []string{item.StageID}, ToPlaylist: opts.ToPlaylist})
}

func (s *Store) stagedImport(ctx context.Context, id int64) (*Item, error) {
	if err := s.requireActive(ctx); err != nil {
		return nil, err
	}
	item, err := scanItem(s.database.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM Item WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	if item.Status != "staged" || item.CancelRequested || item.ContentHash == "" {
		return nil, stateChanged()
	}
	return item, nil
}

// ApplyImport runs inside the caller's Desktop-protected History operation.
// Its durable intent precedes collection writes; its exact Track receipt
// precedes the caller's After snapshot. Staging remains available until later
// explicit cleanup, including after a process death between independent commits.
func (s *Store) ApplyImport(ctx context.Context, d *sql.DB, opts ImportOptions) (*ImportReceipt, error) {
	historyID, err := history.OperationID(ctx, s.library, "intake.import", history.ScopeCollection)
	if err != nil {
		return nil, err
	}
	var receipt *ImportReceipt
	err = s.withWorker(ctx, func(ctx context.Context) error {
		item, err := s.stagedImport(ctx, opts.ID)
		if err != nil {
			return err
		}
		staged := staging.New(s.library)
		selection := staging.ImportOptions{IDs: []string{item.StageID}, ToPlaylist: opts.ToPlaylist, Runner: opts.Runner}
		if _, err := staged.CheckImport(ctx, d, selection, opts.PlanToken); err != nil {
			return err
		}
		result, err := staged.ImportRetained(ctx, d, selection, opts.PlanToken, func(ctx context.Context) error {
			return s.beginImport(ctx, item, historyID, opts)
		})
		if err != nil {
			return err
		}
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := s.recordImport(finish, item.ID, historyID, result.Track.ID); err != nil {
			return errors.Join(err, history.RequireRecovery(ctx))
		}
		receipt = &ImportReceipt{ItemID: item.ID, HistoryID: historyID, TrackID: result.Track.ID}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *Store) recordImport(ctx context.Context, itemID int64, historyID string, trackID int64) error {
	result, err := s.database.ExecContext(ctx, `UPDATE ImportIntent SET track_id=? WHERE item_id=? AND history_id=? AND track_id IS NULL`, trackID, itemID, historyID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return stateChanged()
	}
	return nil
}

func (s *Store) beginImport(ctx context.Context, item *Item, historyID string, opts ImportOptions) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE Item SET status='importing',history_id=? WHERE id=? AND status='staged' AND cancel_requested=0 AND stage_id=? AND content_hash=?`, historyID, item.ID, item.StageID, item.ContentHash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return stateChanged()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ImportIntent(item_id,history_id,plan_token,to_playlist) VALUES(?,?,?,?)`, item.ID, historyID, opts.PlanToken, opts.ToPlaylist); err != nil {
		return err
	}
	return tx.Commit()
}

// ReconcileImports never submits an import. A completed exact History record
// and persisted Track result can finalize a receipt; pending, failed or missing
// evidence requires recovery. Effective Undo cancels the source version;
// Undo-of-Undo restores its imported status without resubmitting any import.
func (s *Store) ReconcileImports(ctx context.Context) error {
	return s.withReconciledImports(ctx, func(context.Context) error { return nil })
}

func (s *Store) withReconciledImports(ctx context.Context, complete func(context.Context) error) error {
	if err := history.CheckSmartlistRepair(ctx, s.library); err != nil {
		return err
	}
	var exists bool
	if err := s.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ImportIntent)`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		// An import publishes its intent under History and wakes reconciliation
		// after releasing it. Empty queues never enroll History just to browse.
		return s.withWorker(ctx, complete)
	}
	err := history.ObserveEntries(ctx, s.library, func(ctx context.Context, entries []history.Entry) error {
		// Match import's History -> worker order. Fresh receipt tuples are read
		// only after both owners are held, including Track results committed
		// while this observation waited for the importing History owner.
		return s.withWorker(ctx, func(ctx context.Context) error {
			if err := s.reconcileImports(ctx, entries); err != nil {
				return err
			}
			return complete(ctx)
		})
	})
	if errors.Is(err, history.ErrHistoryAbsent) {
		return s.withWorker(ctx, func(ctx context.Context) error {
			// A newly appeared owner must be observed afresh, never treated as
			// missing evidence. Replacement restores cannot run while this Store
			// retains its native lifetime lease.
			if _, statErr := os.Stat(filepath.Join(s.library, ".engine-cli", "history", "lock.db")); !errors.Is(statErr, os.ErrNotExist) {
				if statErr != nil {
					return statErr
				}
				return stateChanged()
			}
			return errors.Join(&progress.Error{Code: "intake_import_recovery_required", Cause: err}, intakecontrol.Set(ctx, s.database, "history_unavailable"))
		})
	}
	return err
}

func (s *Store) reconcileImports(ctx context.Context, entries []history.Entry) error {
	byID := make(map[string]history.Entry, len(entries))
	undone := make(map[string]bool)
	for _, e := range entries {
		byID[e.ID] = e
		if e.State == "complete" && e.UndoOf != "" && !undone[e.ID] {
			undone[e.UndoOf] = true
		}
	}
	rows, err := s.database.QueryContext(ctx, `SELECT item_id,history_id,track_id FROM ImportIntent ORDER BY item_id`)
	if err != nil {
		return err
	}
	var receipts []ImportReceipt
	for rows.Next() {
		var receipt ImportReceipt
		var trackID sql.NullInt64
		if err := rows.Scan(&receipt.ItemID, &receipt.HistoryID, &trackID); err != nil {
			return errors.Join(err, rows.Close())
		}
		receipt.TrackID = trackID.Int64
		receipts = append(receipts, receipt)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, receipt := range receipts {
		state := "imported"
		if undone[receipt.HistoryID] {
			state = "cancelled"
		} else if e, ok := byID[receipt.HistoryID]; !ok || e.State != "complete" || receipt.TrackID <= 0 {
			pauseErr := intakecontrol.Set(ctx, s.database, "history_unavailable")
			return errors.Join(&progress.Error{Code: "intake_import_recovery_required", Cause: fmt.Errorf("intake import has no completed matching collection receipt")}, pauseErr)
		}
		if _, err := s.database.ExecContext(ctx, `UPDATE Item SET status=? WHERE id=? AND history_id=? AND status IN ('importing','imported','cancelled')`, state, receipt.ItemID, receipt.HistoryID); err != nil {
			return err
		}
	}
	return nil
}
