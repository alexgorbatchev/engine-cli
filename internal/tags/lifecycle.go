package tags

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type TrackOrigin struct {
	UUID string
	ID   int64
}

// ReconcileRemoved prunes only identities present in the operation's before
// snapshot and removed by that operation. Older orphans require explicit review.
func ReconcileRemoved(ctx context.Context, library, before string) error {
	s, err := Read(ctx, library)
	if err != nil || len(s.Assignments) == 0 {
		return err
	}
	previous, err := readOrigins(ctx, before)
	if err != nil {
		return err
	}
	present, err := readOrigins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return err
	}
	expected := revision(s)
	s.Assignments = slices.DeleteFunc(s.Assignments, func(a Assignment) bool {
		origin := TrackOrigin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		return previous[origin] && !present[origin]
	})
	if expected == revision(s) {
		return nil
	}
	_, err = Replace(ctx, library, expected, s)
	return err
}

func readOrigins(ctx context.Context, path string) (map[TrackOrigin]bool, error) {
	d, err := db.Open(path, true)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, "SELECT originDatabaseUuid,originTrackId FROM Track")
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	present := map[TrackOrigin]bool{}
	for rows.Next() {
		var origin TrackOrigin
		if err := rows.Scan(&origin.UUID, &origin.ID); err != nil {
			return nil, errors.Join(err, rows.Close(), d.Close())
		}
		present[origin] = true
	}
	if err := errors.Join(rows.Err(), rows.Close(), d.Close()); err != nil {
		return nil, err
	}
	return present, nil
}

// MergeOrigins unions all removed tracks' values onto the selected survivor.
// IDs remain local to this library; no path or foreign UUID aliases are added.
func MergeOrigins(ctx context.Context, library string, keep TrackOrigin, removed []TrackOrigin) error {
	s, err := Read(ctx, library)
	if err != nil || len(s.Assignments) == 0 {
		return err
	}
	expected := revision(s)
	seen := map[Assignment]bool{}
	assignments := make([]Assignment, 0, len(s.Assignments))
	for _, a := range s.Assignments {
		if slices.Contains(removed, TrackOrigin{UUID: a.OriginUUID, ID: a.OriginTrackID}) {
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
