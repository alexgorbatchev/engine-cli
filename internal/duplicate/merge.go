package duplicate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/fingerprint"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

type MergeOptions struct {
	Keep               int64    `json:"keep"`
	Remove             []int64  `json:"remove"`
	PerformanceFrom    int64    `json:"performance_from"`
	KeepRef            string   `json:"keep_ref"`
	RemoveRefs         []string `json:"remove_refs"`
	PerformanceFromRef string   `json:"performance_from_ref"`
}

type MergeResult struct {
	Kept    Track   `json:"kept"`
	Removed []Track `json:"removed"`
}

// Merge commits Engine records, then merges companion assignments. Audio remains
// at its original path. The caller records both stores in history; a companion
// publication failure retains that pending record for explicit recovery.
func Merge(ctx context.Context, d *sql.DB, opts MergeOptions) (*MergeResult, error) {
	var err error
	opts, err = resolveMerge(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // Best effort after commit or an earlier error.
	tracks, err := readTracks(ctx, tx)
	if err != nil {
		return nil, err
	}
	result, err := selection(ctx, lib, tracks, opts)
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
	removed := make([]tags.TrackOrigin, 0, len(result.Removed))
	for _, track := range result.Removed {
		removed = append(removed, tags.TrackOrigin{UUID: track.originUUID, ID: track.originID})
	}
	if err := tags.MergeOrigins(ctx, lib, tags.TrackOrigin{UUID: result.Kept.originUUID, ID: result.Kept.originID}, removed); err != nil {
		return nil, errors.Join(err, history.RequireRecovery(ctx))
	}
	return result, nil
}

func resolveMerge(ctx context.Context, d *sql.DB, o MergeOptions) (MergeOptions, error) {
	if (o.KeepRef != "" && o.Keep != 0) || (len(o.RemoveRefs) > 0 && len(o.Remove) > 0) || (o.PerformanceFromRef != "" && o.PerformanceFrom != 0) {
		return o, fmt.Errorf("choose references or numeric IDs for each selection")
	}
	for _, selection := range []struct {
		ref string
		id  *int64
	}{{o.KeepRef, &o.Keep}, {o.PerformanceFromRef, &o.PerformanceFrom}} {
		if selection.ref != "" {
			id, err := identity.ResolveDatabase(ctx, d, "track", selection.ref)
			if err != nil {
				return o, err
			}
			*selection.id = id
		}
	}
	for _, ref := range o.RemoveRefs {
		id, err := identity.ResolveDatabase(ctx, d, "track", ref)
		if err != nil {
			return o, err
		}
		o.Remove = append(o.Remove, id)
	}
	return o, nil
}

func selection(ctx context.Context, lib string, tracks []Track, opts MergeOptions) (*MergeResult, error) {
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
			match, err := audioCandidate(ctx, lib, keep, tr)
			if err != nil {
				return nil, err
			}
			if !match {
				return nil, fmt.Errorf("track %d is not a duplicate candidate of track %d; scan audio before merging recordings with different metadata", id, opts.Keep)
			}
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

func audioCandidate(ctx context.Context, lib string, a, b Track) (bool, error) {
	index, err := fingerprint.Load(lib)
	if err != nil {
		return false, err
	}
	first, fa, err := audioItem(ctx, lib, a, index, false)
	if err != nil || first.Error != nil {
		return false, err
	}
	second, fb, err := audioItem(ctx, lib, b, index, false)
	if err != nil || second.Error != nil {
		return false, err
	}
	m, err := fingerprint.Compare(ctx, fa, fb)
	return m.Duplicate, err
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
