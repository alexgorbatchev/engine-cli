package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CollectionTags is a single companion observation resolved against the current
// collection. Tracks without assignments are omitted; row IDs are library-local.
type CollectionTags struct {
	*Taxonomy
	Tracks []TrackValues `json:"tracks"`
}

type TrackValues struct {
	TrackID int64    `json:"track_id"`
	Values  []string `json:"values"`
}

// ListTracks never enrolls companion storage. The caller owns the collection read
// barrier; one companion read binds definitions and assignments to one revision.
func ListTracks(ctx context.Context, engine *sql.DB, library string) (*CollectionTags, error) {
	state, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	values := map[TrackOrigin][]string{}
	for _, assignment := range state.Assignments {
		origin := TrackOrigin{UUID: assignment.OriginUUID, ID: assignment.OriginTrackID}
		values[origin] = append(values[origin], assignment.ValueID)
	}
	tracks, present, err := collectionValues(ctx, engine, values)
	if err != nil {
		return nil, err
	}
	return &CollectionTags{Taxonomy: taxonomy(state, present), Tracks: tracks}, nil
}

func collectionValues(ctx context.Context, engine *sql.DB, values map[TrackOrigin][]string) ([]TrackValues, map[TrackOrigin]bool, error) {
	rows, err := engine.QueryContext(ctx, "SELECT id,originDatabaseUuid,originTrackId FROM Track ORDER BY id")
	if err != nil {
		return nil, nil, fmt.Errorf("reading custom-tag collection identities: %w", err)
	}
	tracks := []TrackValues{}
	present := map[TrackOrigin]bool{}
	for rows.Next() {
		var id int64
		var origin TrackOrigin
		if err := rows.Scan(&id, &origin.UUID, &origin.ID); err != nil {
			return nil, nil, errors.Join(err, rows.Close())
		}
		present[origin] = true
		if assigned := values[origin]; len(assigned) != 0 {
			tracks = append(tracks, TrackValues{TrackID: id, Values: assigned})
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	return tracks, present, nil
}
