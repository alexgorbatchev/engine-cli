package rekordbox

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

type trackDisposition struct {
	retained nativeTrack
	prepared *preparedTrack
	omit     bool
}

type nativePlan struct {
	source  source
	name    string
	archive importsource.State
	tracks  map[string]trackDisposition
}

type nativeResult struct {
	rootID                   int64
	created, reused, skipped int
	archive                  importsource.State
}

// writeReviewed runs inside the caller's transaction after its immutable source,
// media and collection observations have been revalidated. Companion publication
// follows the Engine commit under the same history owner, retaining recovery on
// a failed independent publication. This function never decodes or commits.
func writeReviewed(ctx context.Context, tx *sql.Tx, plan nativePlan) (nativeResult, error) {
	if err := validateDispositions(plan); err != nil {
		return nativeResult{}, err
	}
	nodes, err := sourceHierarchy(plan.source)
	if err != nil {
		return nativeResult{}, err
	}
	if _, err := destinationIdentity(ctx, tx, plan.name); err != nil {
		return nativeResult{}, err
	}
	tracks, result, err := writeDispositions(ctx, tx, plan)
	if err != nil {
		return nativeResult{}, err
	}
	result.archive, err = mergeArchive(plan.source, plan.archive, tracks)
	if err != nil {
		return nativeResult{}, err
	}
	result.rootID, err = db.CreatePlaylist(ctx, tx, plan.name, 0, true)
	if err != nil {
		return nativeResult{}, err
	}
	ids := make(map[string]int64, len(tracks))
	for selector, track := range tracks {
		ids[selector] = track.id
	}
	if err := writeHierarchy(ctx, tx, result.rootID, nodes, ids); err != nil {
		return nativeResult{}, err
	}
	return result, nil
}

func validateDispositions(plan nativePlan) error {
	if len(plan.tracks) != len(plan.source.selectors) {
		return importError(staleCode, "source track dispositions changed")
	}
	for _, selector := range plan.source.selectors {
		track, ok := plan.tracks[selector]
		choices := 0
		if track.omit {
			choices++
		}
		if track.prepared != nil {
			choices++
		}
		if track.retained.id != 0 {
			choices++
		}
		if !ok || choices != 1 {
			return importError(staleCode, "source track has no unambiguous reviewed disposition")
		}
	}
	return nil
}

func writeDispositions(ctx context.Context, tx *sql.Tx, plan nativePlan) (map[string]nativeTrack, nativeResult, error) {
	tracks := make(map[string]nativeTrack, len(plan.tracks))
	result := nativeResult{}
	for _, selector := range plan.source.selectors {
		if err := ctx.Err(); err != nil {
			return nil, nativeResult{}, err
		}
		entry := plan.tracks[selector]
		if entry.omit {
			tracks[selector] = nativeTrack{}
			result.skipped++
			continue
		}
		id := entry.retained.id
		if entry.prepared != nil {
			var err error
			id, err = insertPrepared(ctx, tx, *entry.prepared)
			if err != nil {
				return nil, nativeResult{}, err
			}
			result.created++
		} else {
			result.reused++
		}
		track, err := readWrittenTrack(ctx, tx, id)
		if err != nil {
			return nil, nativeResult{}, err
		}
		if entry.prepared == nil && track != entry.retained {
			return nil, nativeResult{}, importError(staleCode, "retained track changed after review")
		}
		tracks[selector] = track
	}
	return tracks, result, nil
}

func readWrittenTrack(ctx context.Context, tx *sql.Tx, id int64) (nativeTrack, error) {
	track := nativeTrack{id: id}
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(path,''),COALESCE(originDatabaseUuid,''),COALESCE(originTrackId,0) FROM Track WHERE id=?`, id).Scan(&track.path, &track.origin.uuid, &track.origin.id)
	if err != nil {
		return nativeTrack{}, fmt.Errorf("reading imported native identity: %w", err)
	}
	return track, nil
}
