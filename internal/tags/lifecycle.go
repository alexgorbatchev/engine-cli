package tags

import (
	"context"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

// ReconcileRemoved prunes only identities present in the operation's before
// snapshot and removed by that operation. Older orphans require explicit review.
func ReconcileRemoved(ctx context.Context, library, before string) error {
	s, err := Read(ctx, library)
	if err != nil || len(s.Assignments) == 0 {
		return err
	}
	removed, err := companion.RemovedOrigins(ctx, library, before)
	if err != nil {
		return err
	}
	expected := revision(s)
	s.Assignments = slices.DeleteFunc(s.Assignments, func(a Assignment) bool {
		origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		return removed[origin]
	})
	if expected == revision(s) {
		return nil
	}
	_, err = Replace(ctx, library, expected, s)
	return err
}

// MergeOrigins unions all removed tracks' values onto the selected survivor.
// IDs remain local to this library; no path or foreign UUID aliases are added.
func MergeOrigins(ctx context.Context, library string, keep companion.Origin, removed []companion.Origin) error {
	s, err := Read(ctx, library)
	if err != nil || len(s.Assignments) == 0 {
		return err
	}
	expected := revision(s)
	seen := map[Assignment]bool{}
	assignments := make([]Assignment, 0, len(s.Assignments))
	for _, a := range s.Assignments {
		if slices.Contains(removed, companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}) {
			a.OriginUUID, a.OriginTrackID = keep.UUID, keep.ID
		}
		if !seen[a] {
			seen[a] = true
			assignments = append(assignments, a)
		}
	}
	s.Assignments = assignments
	if expected == revision(s) {
		return nil
	}
	_, err = Replace(ctx, library, expected, s)
	return err
}
