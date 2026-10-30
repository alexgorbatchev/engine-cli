package playhistory

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// historyTrack is the identity of a track copy in hm.db.
type historyTrack struct {
	path       sql.NullString
	originID   sql.NullInt64
	originUUID sql.NullString
}

type origin struct {
	uuid string
	id   int64
}

// libraryTracks indexes the library's tracks by the identities history copies keep.
type libraryTracks struct {
	byPath   map[string]int64
	byOrigin map[origin]int64
}

func readLibraryTracks(ctx context.Context, library db.Querier) (libraryTracks, error) {
	tracks := libraryTracks{byPath: map[string]int64{}, byOrigin: map[origin]int64{}}
	rows, err := library.QueryContext(ctx, "SELECT id, path, originTrackId, originDatabaseUuid FROM Track")
	if err != nil {
		return tracks, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t historyTrack
		if err := rows.Scan(&id, &t.path, &t.originID, &t.originUUID); err != nil {
			return tracks, err
		}
		if t.path.Valid && t.path.String != "" {
			tracks.byPath[t.path.String] = id
		}
		if key, ok := t.origin(); ok {
			tracks.byOrigin[key] = id
		}
	}
	return tracks, rows.Err()
}

func (t historyTrack) origin() (origin, bool) {
	if !t.originID.Valid || t.originID.Int64 <= 0 || !t.originUUID.Valid || t.originUUID.String == "" {
		return origin{}, false
	}
	return origin{t.originUUID.String, t.originID.Int64}, true
}

// resolve matches a history copy by path first, then by origin identity, as
// Engine resolves history entries when importing them as a playlist.
func (l libraryTracks) resolve(t historyTrack) (int64, bool) {
	if t.path.Valid && t.path.String != "" {
		if id, ok := l.byPath[t.path.String]; ok {
			return id, true
		}
	}
	if key, ok := t.origin(); ok {
		id, ok := l.byOrigin[key]
		return id, ok
	}
	return 0, false
}
