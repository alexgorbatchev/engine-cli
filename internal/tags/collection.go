package tags

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
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
	values := map[companion.Origin][]string{}
	for _, assignment := range state.Assignments {
		origin := companion.Origin{UUID: assignment.OriginUUID, ID: assignment.OriginTrackID}
		values[origin] = append(values[origin], assignment.ValueID)
	}
	tracks, present, err := collectionValues(ctx, engine, values)
	if err != nil {
		return nil, err
	}
	return &CollectionTags{Taxonomy: taxonomy(state, present), Tracks: tracks}, nil
}

func collectionValues(ctx context.Context, engine *sql.DB, values map[companion.Origin][]string) ([]TrackValues, map[companion.Origin]bool, error) {
	identities, err := companion.Tracks(ctx, engine)
	if err != nil {
		return nil, nil, fmt.Errorf("reading custom-tag collection identities: %w", err)
	}
	tracks := []TrackValues{}
	present := map[companion.Origin]bool{}
	for _, track := range identities {
		present[track.Origin] = true
		if assigned := values[track.Origin]; len(assigned) != 0 {
			tracks = append(tracks, TrackValues{TrackID: track.ID, Values: assigned})
		}
	}
	return tracks, present, nil
}
