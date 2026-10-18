package duplicate

import (
	"context"
	"database/sql"
	"fmt"
)

type MergeOptions struct {
	Keep            int64   `json:"keep"`
	Remove          []int64 `json:"remove"`
	PerformanceFrom int64   `json:"performance_from"`
}

type MergeResult struct {
	Kept    Track   `json:"kept"`
	Removed []Track `json:"removed"`
}

// Merge changes database records atomically. Audio remains at its original path.
// The caller records history and snapshots before invoking any mutation.
func Merge(ctx context.Context, d *sql.DB, opts MergeOptions) (*MergeResult, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // Best effort after commit or an earlier error.
	tracks, err := readTracks(ctx, tx)
	if err != nil {
		return nil, err
	}
	result, err := selection(tracks, opts)
	if err != nil {
		return nil, err
	}
	if opts.PerformanceFrom != 0 {
		for _, id := range []int64{opts.Keep, opts.PerformanceFrom} {
			var exists bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM PerformanceData WHERE trackId=?)", id).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("track %d has no performance data to transfer; repair it before merging", id)
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE PerformanceData SET (trackData,overviewWaveFormData,beatData,quickCues,loops,thirdPartySourceId,activeOnLoadLoops)=
		(SELECT trackData,overviewWaveFormData,beatData,quickCues,loops,thirdPartySourceId,activeOnLoadLoops FROM PerformanceData WHERE trackId=?) WHERE trackId=?`, opts.PerformanceFrom, opts.Keep)
		if err != nil {
			return nil, fmt.Errorf("transferring selected performance data: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE Track SET isAnalyzed=(SELECT isAnalyzed FROM Track WHERE id=?),isBeatGridLocked=(SELECT isBeatGridLocked FROM Track WHERE id=?) WHERE id=?`, opts.PerformanceFrom, opts.PerformanceFrom, opts.Keep); err != nil {
			return nil, err
		}
	}
	for _, source := range result.Removed {
		if err = mergeMemberships(ctx, tx, result.Kept, source); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE Track SET timeLastPlayed=max(COALESCE(timeLastPlayed,0),?),isPlayed=COALESCE(isPlayed,0) OR ? WHERE id=?`, source.TimeLastPlayed, source.IsPlayed, opts.Keep); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM PerformanceData WHERE trackId=?", source.ID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM Track WHERE id=?", source.ID); err != nil {
			return nil, err
		}
	}
	tracks, err = readTracks(ctx, tx)
	if err != nil {
		return nil, err
	}
	result.Kept, err = lookup(tracks, opts.Keep)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func selection(tracks []Track, opts MergeOptions) (*MergeResult, error) {
	keep, err := lookup(tracks, opts.Keep)
	if err != nil {
		return nil, err
	}
	if len(opts.Remove) == 0 {
		return nil, fmt.Errorf("select at least one duplicate track to remove")
	}
	allowed := make(map[int64]bool)
	for _, group := range groupTracks(tracks, "all") {
		contains := false
		for _, tr := range group.Tracks {
			if tr.ID == opts.Keep {
				contains = true
			}
		}
		if contains {
			for _, tr := range group.Tracks {
				allowed[tr.ID] = true
			}
		}
	}
	seen := map[int64]bool{opts.Keep: true}
	result := &MergeResult{Kept: keep}
	for _, id := range opts.Remove {
		if seen[id] {
			return nil, fmt.Errorf("track %d is selected more than once or is the keeper", id)
		}
		seen[id] = true
		tr, err := lookup(tracks, id)
		if err != nil {
			return nil, err
		}
		if !allowed[id] {
			return nil, fmt.Errorf("track %d is not a duplicate candidate of track %d", id, opts.Keep)
		}
		if tr.originID <= 0 || tr.originUUID == "" {
			return nil, fmt.Errorf("track %d has no persistent Engine identity", id)
		}
		result.Removed = append(result.Removed, tr)
	}
	if keep.originID <= 0 || keep.originUUID == "" {
		return nil, fmt.Errorf("keeper has no persistent Engine identity")
	}
	if opts.PerformanceFrom != 0 && !seen[opts.PerformanceFrom] {
		return nil, fmt.Errorf("performance source must be one of the selected tracks")
	}
	return result, nil
}

func mergeMemberships(ctx context.Context, tx *sql.Tx, keep, source Track) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,listId,nextEntityId FROM PlaylistEntity WHERE trackId=? AND databaseUuid=? ORDER BY id", source.originID, source.originUUID)
	if err != nil {
		return err
	}
	type membership struct {
		id, list int64
		next     sql.NullInt64
	}
	var members []membership
	for rows.Next() {
		var m membership
		if err = rows.Scan(&m.id, &m.list, &m.next); err != nil {
			rows.Close()
			return err
		}
		members = append(members, m)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for _, m := range members {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM PlaylistEntity WHERE listId=? AND trackId=? AND databaseUuid=?)", m.list, keep.originID, keep.originUUID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err = tx.ExecContext(ctx, "UPDATE PlaylistEntity SET nextEntityId=? WHERE listId=? AND nextEntityId=?", m.next, m.list, m.id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM PlaylistEntity WHERE id=?", m.id); err != nil {
				return err
			}
		} else if _, err = tx.ExecContext(ctx, "UPDATE PlaylistEntity SET trackId=?,databaseUuid=? WHERE id=?", keep.originID, keep.originUUID, m.id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM PreparelistEntity WHERE trackId=? AND EXISTS(SELECT 1 FROM PreparelistEntity WHERE trackId=?)", source.ID, keep.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE PreparelistEntity SET trackId=? WHERE trackId=?", keep.ID, source.ID)
	return err
}
