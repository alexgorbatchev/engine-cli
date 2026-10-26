package db

import (
	"context"
	"encoding/json"
	"fmt"
)

// PlaylistMembership identifies a playlist that directly contains a track.
type PlaylistMembership struct {
	ID    int64
	Title string
}

// PlaylistMemberships maps each listed local track ID to the playlists that
// directly contain it, ordered by playlist ID without repeats. Engine stores
// memberships against a track's persistent origin identity, so local IDs of
// imported or copied tracks never match PlaylistEntity rows directly. Tracks
// in no playlist have no entry. The IDs travel as one JSON array parameter so a
// whole collection needs a single query regardless of SQLite variable limits.
func PlaylistMemberships(ctx context.Context, q Querier, trackIDs []int64) (map[int64][]PlaylistMembership, error) {
	memberships := make(map[int64][]PlaylistMembership)
	if len(trackIDs) == 0 {
		return memberships, nil
	}
	ids, err := json.Marshal(trackIDs)
	if err != nil {
		return nil, fmt.Errorf("encoding track IDs: %w", err)
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT t.id, p.id, COALESCE(p.title,'') FROM Track t
	JOIN PlaylistEntity pe ON pe.trackId=t.originTrackId AND pe.databaseUuid=t.originDatabaseUuid
	JOIN Playlist p ON p.id=pe.listId
	WHERE t.id IN (SELECT value FROM json_each(?))
	ORDER BY t.id, p.id`, string(ids))
	if err != nil {
		return nil, fmt.Errorf("reading playlist memberships: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var trackID int64
		var m PlaylistMembership
		if err := rows.Scan(&trackID, &m.ID, &m.Title); err != nil {
			return nil, fmt.Errorf("scanning playlist membership: %w", err)
		}
		memberships[trackID] = append(memberships[trackID], m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating playlist memberships: %w", err)
	}
	return memberships, nil
}
