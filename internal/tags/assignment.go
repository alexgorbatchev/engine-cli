package tags

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func trackOrigin(ctx context.Context, engine *sql.DB, selector string) (int64, string, int64, error) {
	t, err := companion.ResolveTrack(ctx, engine, selector)
	if err != nil {
		return 0, "", 0, err
	}
	if t.Origin.UUID == "" || t.Origin.ID <= 0 {
		return 0, "", 0, failure("tag_track_identity_invalid", "the selected track has no stable origin identity")
	}
	return t.ID, t.Origin.UUID, t.Origin.ID, nil
}

func InspectTrack(ctx context.Context, engine *sql.DB, library, selector string) (*TrackTags, error) {
	id, origin, originID, err := trackOrigin(ctx, engine, selector)
	if err != nil {
		return nil, err
	}
	s, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	values := []string{}
	for _, a := range s.Assignments {
		if a.OriginUUID == origin && a.OriginTrackID == originID {
			values = append(values, a.ValueID)
		}
	}
	return &TrackTags{TrackID: id, Revision: revision(s), Values: values}, nil
}

func Assign(ctx context.Context, engine *sql.DB, library string, edit AssignmentEdit) (*TrackTags, error) {
	if err := ValidateAssignment(edit); err != nil {
		return nil, err
	}
	id, origin, originID, err := trackOrigin(ctx, engine, edit.Track)
	if err != nil {
		return nil, err
	}
	values := slices.Clone(edit.Values)
	slices.Sort(values)
	result, err := mutate(ctx, library, edit.Revision, func(tx *sql.Tx, s State) (string, bool, error) {
		known := map[string]bool{}
		for _, c := range s.Categories {
			for _, v := range c.Values {
				known[v.ID] = true
			}
		}
		for _, v := range values {
			if !known[v] {
				return "", false, failure("tag_not_found", "a selected custom-tag value no longer exists in this library")
			}
		}
		previous := []string{}
		for _, a := range s.Assignments {
			if a.OriginUUID == origin && a.OriginTrackID == originID {
				previous = append(previous, a.ValueID)
			}
		}
		if slices.Equal(previous, values) {
			return fmt.Sprint(id), false, nil
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM Assignment WHERE originUUID=? AND originTrackId=?", origin, originID); err != nil {
			return "", false, err
		}
		for _, v := range values {
			if _, err := tx.ExecContext(ctx, "INSERT INTO Assignment VALUES(?,?,?)", origin, originID, v); err != nil {
				return "", false, err
			}
		}
		return fmt.Sprint(id), true, nil
	})
	if err != nil {
		return nil, err
	}
	return &TrackTags{TrackID: id, Revision: result.Revision, Values: values}, nil
}
