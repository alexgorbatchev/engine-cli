package colors

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

type Collection struct {
	Revision          string       `json:"revision"`
	Tracks            []TrackValue `json:"tracks"`
	UnavailableTracks int          `json:"unavailable_tracks"`
}

type TrackValue struct {
	TrackID int64  `json:"track_id"`
	Color   string `json:"color"`
}

// ListTracks binds every projected row to one stored observation. It does not
// enroll storage or remove colors belonging to externally removed tracks.
func ListTracks(ctx context.Context, engine *sql.DB, library string) (*Collection, error) {
	state, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	tracks, err := companion.Tracks(ctx, engine)
	if err != nil {
		return nil, err
	}
	assigned := map[companion.Origin]string{}
	for _, a := range state.Assignments {
		assigned[companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}] = a.Color
	}
	result := &Collection{Revision: state.Revision(), Tracks: []TrackValue{}}
	present := map[companion.Origin]bool{}
	for _, track := range tracks {
		present[track.Origin] = true
		if color := assigned[track.Origin]; color != "" {
			result.Tracks = append(result.Tracks, TrackValue{TrackID: track.ID, Color: color})
		}
	}
	for origin := range assigned {
		if !present[origin] {
			result.UnavailableTracks++
		}
	}
	return result, nil
}
