package companion

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// Origin is Engine's library-local persistent identity, never a path alias.
type Origin struct {
	UUID string
	ID   int64
}

type Track struct {
	ID int64
	Origin
}

func ResolveTrack(ctx context.Context, engine *sql.DB, selector string) (Track, error) {
	t, err := db.FindTrack(ctx, engine, selector)
	if err != nil {
		return Track{}, err
	}
	if t == nil {
		return Track{}, &progress.Error{Code: "track_not_found", Cause: errors.New("the selected track no longer exists")}
	}
	track := Track{ID: t.ID}
	if err := engine.QueryRowContext(ctx, "SELECT originDatabaseUuid,originTrackId FROM Track WHERE id=?", t.ID).Scan(&track.Origin.UUID, &track.Origin.ID); err != nil {
		return Track{}, err
	}
	return track, nil
}

func Tracks(ctx context.Context, engine *sql.DB) ([]Track, error) {
	rows, err := engine.QueryContext(ctx, "SELECT id,originDatabaseUuid,originTrackId FROM Track ORDER BY id")
	if err != nil {
		return nil, err
	}
	tracks := []Track{}
	for rows.Next() {
		var track Track
		if err := rows.Scan(&track.ID, &track.Origin.UUID, &track.Origin.ID); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		tracks = append(tracks, track)
	}
	return tracks, errors.Join(rows.Err(), rows.Close())
}

func Origins(ctx context.Context, path string) (map[Origin]bool, error) {
	d, err := db.Open(path, true)
	if err != nil {
		return nil, err
	}
	tracks, err := Tracks(ctx, d)
	if err := errors.Join(err, d.Close()); err != nil {
		return nil, err
	}
	present := map[Origin]bool{}
	for _, track := range tracks {
		present[track.Origin] = true
	}
	return present, nil
}

// RemovedOrigins compares the operation's Engine snapshot with its current
// collection. Pre-existing unavailable origins are deliberately not removed.
func RemovedOrigins(ctx context.Context, library, before string) (map[Origin]bool, error) {
	previous, err := Origins(ctx, before)
	if err != nil {
		return nil, err
	}
	present, err := Origins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, err
	}
	for origin := range present {
		delete(previous, origin)
	}
	return previous, nil
}
