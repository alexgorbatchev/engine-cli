package playhistory

import (
	"context"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// Plays summarizes one library track's recorded history.
type Plays struct {
	Count int
	// LastPlayed is the latest timed play in Unix seconds, or 0 when no play has a time.
	LastPlayed int64
}

// Counts maps library track IDs to their plays across every listed session.
// Engine stores no play count of its own; it counts history entries, so this
// does too. A nil map means the library has no history, unlike an empty one.
func Counts(ctx context.Context, libraryDir string, library db.Querier) (map[int64]Plays, error) {
	history, err := Open(libraryDir)
	if err != nil || history == nil {
		return nil, err
	}
	defer history.Close()
	tracks, err := readLibraryTracks(ctx, library)
	if err != nil {
		return nil, err
	}
	counts, err := countPlays(ctx, history, tracks)
	if err != nil {
		return nil, unreadable(err)
	}
	return counts, nil
}

// TrackCounts is Counts for track records, where plays are optional: a history
// database that cannot be read reports unknown plays instead of failing the
// collection read. The session reads report that failure. Only cancellation
// and library read failures propagate.
func TrackCounts(ctx context.Context, libraryDir string, library db.Querier) (map[int64]Plays, error) {
	counts, err := Counts(ctx, libraryDir, library)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return counts, err
	}
	if progress.ErrorCode(err) == "play_history_unreadable" {
		return nil, nil
	}
	return nil, err
}

func countPlays(ctx context.Context, history db.Querier, tracks libraryTracks) (map[int64]Plays, error) {
	// Sessions without a timezone have no reliable times, which Engine shows as none.
	rows, err := history.QueryContext(ctx, `SELECT t.path, t.originTrackId, t.originDatabaseUuid, COUNT(*),
		COALESCE(MAX(CASE WHEN l.timezone IS NOT NULL AND e.startTime > 0 THEN e.startTime END), 0)
		FROM HistorylistEntity e JOIN Historylist l ON l.id = e.listId AND l.isDeleted = 0
		JOIN Track t ON t.id = e.trackId GROUP BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[int64]Plays{}
	for rows.Next() {
		var copied historyTrack
		var count int
		var last int64
		if err := rows.Scan(&copied.path, &copied.originID, &copied.originUUID, &count, &last); err != nil {
			return nil, err
		}
		id, ok := tracks.resolve(copied)
		if !ok {
			continue
		}
		// Several copies can resolve to one library track, for example after a relocation.
		plays := counts[id]
		plays.Count += count
		plays.LastPlayed = max(plays.LastPlayed, last)
		counts[id] = plays
	}
	return counts, rows.Err()
}
