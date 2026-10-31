package tags

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func deletion(ctx context.Context, library string, state State, options DeleteOptions, category bool) (*Deletion, error) {
	if err := checkRevision(options.Revision, state); err != nil {
		return nil, err
	}
	values := map[string]bool{}
	found := false
	for _, c := range state.Categories {
		if category && c.ID == options.ID {
			found = true
		}
		for _, v := range c.Values {
			if category && c.ID == options.ID || !category && v.ID == options.ID {
				found = true
				values[v.ID] = true
			}
		}
	}
	if !found {
		return nil, failure("tag_not_found", "the custom-tag category or value no longer exists")
	}
	result := &Deletion{ID: options.ID, Revision: revision(state), RemovedValues: len(values)}
	present, err := companion.Origins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, err
	}
	tracks := map[companion.Origin]bool{}
	liveAssignments := []Assignment{}
	for _, a := range state.Assignments {
		if values[a.ValueID] {
			result.RemovedAssignments++
			origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
			if present[origin] {
				tracks[origin] = true
				liveAssignments = append(liveAssignments, a)
			}
		}
	}
	result.AffectedTracks = len(tracks)
	// Include deletion kind as well as state, preventing a reviewed value token
	// from authorizing a category deletion with a different affected set. Native
	// origin restoration changes impact without changing companion bytes.
	result.PlanToken = revision(State{Categories: []Category{{ID: fmt.Sprintf("%t:%s:%s", category, options.ID, result.Revision)}}, Assignments: liveAssignments})
	return result, nil
}

func Delete(ctx context.Context, library string, options DeleteOptions, category bool) (*Deletion, error) {
	s, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	result, err := deletion(ctx, library, s, options, category)
	if err != nil || options.DryRun {
		return result, err
	}
	if options.PlanToken == "" || options.PlanToken != result.PlanToken {
		return nil, failure("tag_plan_stale", "review the current custom-tag deletion before applying it")
	}
	mutation, err := mutate(ctx, library, options.Revision, func(tx *sql.Tx, current State) (string, bool, error) {
		checked, err := deletion(ctx, library, current, options, category)
		if err != nil {
			return "", false, err
		}
		if checked.PlanToken != options.PlanToken {
			return "", false, failure("tag_plan_stale", "custom-tag deletion impact changed; review it again")
		}
		table := "Value"
		if category {
			table = "Category"
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=?", options.ID)
		return options.ID, true, err
	})
	if err != nil {
		return nil, err
	}
	result.Applied = true
	result.Revision = mutation.Revision
	return result, nil
}
