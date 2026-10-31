package colors

import (
	"context"
	"database/sql"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

type TrackColor struct {
	TrackID  int64  `json:"track_id"`
	Color    string `json:"color"`
	Revision string `json:"revision"`
	Changed  bool   `json:"changed"`
}

type Edit struct {
	Track    string
	Color    string
	Revision string
}

func InspectTrack(ctx context.Context, engine *sql.DB, library, selector string) (*TrackColor, error) {
	track, err := resolveTrack(ctx, engine, selector)
	if err != nil {
		return nil, err
	}
	state, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	return trackColor(state, track), nil
}

func resolveTrack(ctx context.Context, engine *sql.DB, selector string) (companion.Track, error) {
	track, err := companion.ResolveTrack(ctx, engine, selector)
	if err != nil {
		return companion.Track{}, err
	}
	if track.Origin.UUID == "" || track.Origin.ID <= 0 {
		return companion.Track{}, failure("color_track_identity_invalid", "the selected track has no stable origin identity")
	}
	return track, nil
}

func trackColor(state State, track companion.Track) *TrackColor {
	result := &TrackColor{TrackID: track.ID, Revision: state.Revision()}
	for _, assignment := range state.Assignments {
		if assignment.OriginUUID == track.Origin.UUID && assignment.OriginTrackID == track.Origin.ID {
			result.Color = assignment.Color
			break
		}
	}
	return result
}

// CheckEdit runs before safety enrollment; Assign repeats the same checks under
// the collection write barrier and Replace rechecks the stored revision in SQL.
func CheckEdit(ctx context.Context, engine *sql.DB, library string, edit Edit) error {
	state, track, color, err := checkedEdit(ctx, engine, library, edit)
	if err != nil {
		return err
	}
	if trackColor(state, track).Color == color {
		return failure("color_no_changes", "the selected track already has this color")
	}
	return nil
}

func checkedEdit(ctx context.Context, engine *sql.DB, library string, edit Edit) (State, companion.Track, string, error) {
	color, err := normalize(edit.Color)
	if err != nil {
		return State{}, companion.Track{}, "", err
	}
	track, err := resolveTrack(ctx, engine, edit.Track)
	if err != nil {
		return State{}, companion.Track{}, "", err
	}
	state, err := Read(ctx, library)
	if err == nil {
		err = checkRevision(edit.Revision, state)
	}
	return state, track, color, err
}

func Assign(ctx context.Context, engine *sql.DB, library string, edit Edit) (*TrackColor, error) {
	state, track, color, err := checkedEdit(ctx, engine, library, edit)
	if err != nil {
		return nil, err
	}
	before := trackColor(state, track)
	if before.Color == color {
		return before, nil
	}
	state.Assignments = slices.DeleteFunc(state.Assignments, func(a Assignment) bool {
		return a.OriginUUID == track.Origin.UUID && a.OriginTrackID == track.Origin.ID
	})
	if color != "" {
		state.Assignments = append(state.Assignments, Assignment{OriginUUID: track.Origin.UUID, OriginTrackID: track.Origin.ID, Color: color})
	}
	state, err = Replace(ctx, library, edit.Revision, state)
	if err != nil {
		return nil, err
	}
	result := trackColor(state, track)
	result.Changed = true
	return result, nil
}
