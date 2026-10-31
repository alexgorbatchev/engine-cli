package colors

import (
	"context"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

// CheckMerge rejects ambiguous survivor colors before collection safety
// enrollment. MergeOrigins repeats the decision before companion publication.
func CheckMerge(ctx context.Context, library string, keep companion.Origin, removed []companion.Origin) error {
	state, err := Read(ctx, library)
	if err != nil {
		return err
	}
	_, err = mergedState(state, keep, removed)
	return err
}

func MergeOrigins(ctx context.Context, library string, keep companion.Origin, removed []companion.Origin) error {
	state, err := Read(ctx, library)
	if err != nil {
		return err
	}
	next, err := mergedState(state, keep, removed)
	if err != nil || next.Revision() == state.Revision() {
		return err
	}
	_, err = Replace(ctx, library, state.Revision(), next)
	return err
}

func mergedState(state State, keep companion.Origin, removed []companion.Origin) (State, error) {
	assigned := ""
	for _, a := range state.Assignments {
		if (companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}) == keep {
			assigned = a.Color
			break
		}
	}
	selected := assigned
	next := State{Assignments: []Assignment{}}
	for _, a := range state.Assignments {
		origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		if origin == keep {
			continue
		}
		if !slices.Contains(removed, origin) {
			next.Assignments = append(next.Assignments, a)
			continue
		}
		if assigned == "" && selected != "" && selected != a.Color {
			return State{}, failure("color_merge_conflict", "the removed tracks have different colors; assign the survivor's color before merging")
		}
		if selected == "" {
			selected = a.Color
		}
	}
	if selected != "" {
		next.Assignments = append(next.Assignments, Assignment{OriginUUID: keep.UUID, OriginTrackID: keep.ID, Color: selected})
	}
	return canonical(next), nil
}
