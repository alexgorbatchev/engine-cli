package tags

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

// MergeOptions folds one value into another of the same category, such as a
// misspelled duplicate into the value it was meant to be: every track holding ID
// holds Into instead, and ID is deleted.
type MergeOptions struct {
	ID        string `json:"id"`
	Into      string `json:"into"`
	Revision  string `json:"revision"`
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token,omitempty"`
}

// Merge is the reviewed, or applied, fold of one value into another.
type Merge struct {
	ID       string `json:"id"`
	Into     string `json:"into"`
	Revision string `json:"revision"`
	// PlanToken binds the values, the stored revision and the live tracks the merge moves.
	PlanToken string `json:"plan_token"`
	// AffectedTracks counts present tracks holding ID; MovedAssignments those of them that do not already hold Into.
	AffectedTracks   int  `json:"affected_tracks"`
	MovedAssignments int  `json:"moved_assignments"`
	Applied          bool `json:"applied"`
}

func valueCategory(state State, id string) (string, bool) {
	for _, c := range state.Categories {
		for _, v := range c.Values {
			if v.ID == id {
				return c.ID, true
			}
		}
	}
	return "", false
}

func merging(ctx context.Context, library string, state State, options MergeOptions) (*Merge, error) {
	if err := checkRevision(options.Revision, state); err != nil {
		return nil, err
	}
	from, ok := valueCategory(state, options.ID)
	into, intoOK := valueCategory(state, options.Into)
	if !ok || !intoOK {
		return nil, failure("tag_not_found", "the custom-tag value no longer exists")
	}
	if options.ID == options.Into || from != into {
		return nil, failure("tag_merge_invalid", "merge a value into another value of its own category")
	}
	present, err := companion.Origins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, err
	}
	holds := map[companion.Origin]map[string]bool{}
	for _, a := range state.Assignments {
		origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		if holds[origin] == nil {
			holds[origin] = map[string]bool{}
		}
		holds[origin][a.ValueID] = true
	}
	result := &Merge{ID: options.ID, Into: options.Into, Revision: revision(state)}
	moved := []Assignment{}
	for _, a := range state.Assignments {
		origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		if a.ValueID != options.ID || !present[origin] {
			continue
		}
		result.AffectedTracks++
		moved = append(moved, a)
		if !holds[origin][options.Into] {
			result.MovedAssignments++
		}
	}
	result.PlanToken = revision(State{Categories: []Category{{ID: fmt.Sprintf("merge:%s:%s:%s", options.ID, options.Into, result.Revision)}}, Assignments: moved})
	return result, nil
}

// CheckMerge validates a merge before the collection pipeline starts it.
func CheckMerge(ctx context.Context, library string, options MergeOptions) error {
	s, err := Read(ctx, library)
	if err != nil {
		return err
	}
	result, err := merging(ctx, library, s, options)
	if err != nil {
		return err
	}
	if !options.DryRun && (options.PlanToken == "" || options.PlanToken != result.PlanToken) {
		return failure("tag_plan_stale", "review the current custom-tag merge before applying it")
	}
	return nil
}

// MergeValue reviews a merge on a dry run, or applies exactly the reviewed merge in one transaction.
func MergeValue(ctx context.Context, library string, options MergeOptions) (*Merge, error) {
	s, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	result, err := merging(ctx, library, s, options)
	if err != nil || options.DryRun {
		return result, err
	}
	if options.PlanToken == "" || options.PlanToken != result.PlanToken {
		return nil, failure("tag_plan_stale", "review the current custom-tag merge before applying it")
	}
	mutation, err := mutate(ctx, library, options.Revision, func(tx *sql.Tx, current State) (string, bool, error) {
		checked, err := merging(ctx, library, current, options)
		if err != nil {
			return "", false, err
		}
		if checked.PlanToken != options.PlanToken {
			return "", false, failure("tag_plan_stale", "custom-tag merge impact changed; review it again")
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO Assignment SELECT originUUID,originTrackId,? FROM Assignment WHERE valueId=?", options.Into, options.ID); err != nil {
			return "", false, err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM Value WHERE id=?", options.ID)
		return options.Into, true, err
	})
	if err != nil {
		return nil, err
	}
	result.Applied = true
	result.Revision = mutation.Revision
	return result, nil
}
