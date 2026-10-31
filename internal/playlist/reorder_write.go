package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ApplyReorder rechecks the exact review and updates only membership links and
// the target playlist timestamp in one transaction. Callers own Desktop checks,
// snapshots and undo history, after CheckReorder succeeds.
func ApplyReorder(ctx context.Context, d *sql.DB, root string, opts ReorderOptions) (*ReorderResult, error) {
	id, err := reorderPlaylistID(ctx, d, opts.Playlist)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, err := planReorder(ctx, tx, root, id, opts)
	if err != nil {
		return nil, err
	}
	if err := checkReorder(plan, opts.PlanToken); err != nil {
		return nil, err
	}
	if err := writeReorder(ctx, tx, plan); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ReorderResult{PlaylistID: id, TrackIDs: plan.TrackIDs}, nil
}

func writeReorder(ctx context.Context, tx *sql.Tx, plan *ReorderPlan) error {
	previous := make(map[int64]int64, len(plan.members))
	for _, m := range plan.members {
		previous[m.ID] = m.Next.Int64
	}
	for i, id := range plan.entities {
		var next int64
		if i+1 < len(plan.entities) {
			next = plan.entities[i+1]
		}
		if previous[id] == next {
			continue
		}
		result, err := tx.ExecContext(ctx, "UPDATE PlaylistEntity SET nextEntityId=? WHERE id=? AND listId=?", next, id, plan.PlaylistID)
		if err != nil {
			return fmt.Errorf("updating playlist membership order: %w", err)
		}
		if err := requireReorderRow(result); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, "UPDATE Playlist SET lastEditTime=? WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), plan.PlaylistID)
	if err != nil {
		return fmt.Errorf("updating playlist order timestamp: %w", err)
	}
	return requireReorderRow(result)
}

func requireReorderRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return playlistFileError("playlist_reorder_stale", "playlist membership changed during the move")
	}
	return nil
}
