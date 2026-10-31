package colors

import (
	"context"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

// ReconcileRemoved preserves earlier unavailable assignments; only origins
// actually removed inside this history boundary are pruned automatically.
func ReconcileRemoved(ctx context.Context, library, before string) error {
	state, err := Read(ctx, library)
	if err != nil || len(state.Assignments) == 0 {
		return err
	}
	removed, err := companion.RemovedOrigins(ctx, library, before)
	if err != nil {
		return err
	}
	expected := state.Revision()
	state.Assignments = slices.DeleteFunc(state.Assignments, func(a Assignment) bool {
		return removed[companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}]
	})
	if state.Revision() == expected {
		return nil
	}
	_, err = Replace(ctx, library, expected, state)
	return err
}
