package tags

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func missingAssignments(state State, present map[companion.Origin]bool) ([]Assignment, int) {
	missing := []Assignment{}
	origins := map[companion.Origin]bool{}
	for _, a := range state.Assignments {
		origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		if !present[origin] {
			missing = append(missing, a)
			origins[origin] = true
		}
	}
	return missing, len(origins)
}

func reconciliation(ctx context.Context, library string, state State, options ReconcileOptions) (*Reconciliation, []Assignment, error) {
	if err := checkRevision(options.Revision, state); err != nil {
		return nil, nil, err
	}
	present, err := companion.Origins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, nil, err
	}
	missing, count := missingAssignments(state, present)
	// Bind both stored revision and exact missing identities. A Desktop change
	// can invalidate the review without changing companion bytes.
	token := revision(State{Categories: []Category{{ID: "reconcile:" + revision(state)}}, Assignments: missing})
	return &Reconciliation{Revision: revision(state), PlanToken: token, UnavailableTracks: count, RemovedAssignments: len(missing)}, missing, nil
}

func CheckReconciliation(ctx context.Context, library string, options ReconcileOptions) error {
	state, err := Read(ctx, library)
	if err != nil {
		return err
	}
	result, _, err := reconciliation(ctx, library, state, options)
	if err != nil || options.DryRun {
		return err
	}
	return checkReconciliationToken(options.PlanToken, result.PlanToken)
}

func checkReconciliationToken(expected, actual string) error {
	if expected == "" || expected != actual {
		return failure("tag_plan_stale", "review the current unavailable-track assignments before removing them")
	}
	return nil
}

// Reconcile previews or explicitly removes unavailable-track assignments. Reads
// retain them, so undo and identity restoration remain possible until consent.
func Reconcile(ctx context.Context, library string, options ReconcileOptions) (*Reconciliation, error) {
	state, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	result, _, err := reconciliation(ctx, library, state, options)
	if err != nil || options.DryRun {
		return result, err
	}
	if err := checkReconciliationToken(options.PlanToken, result.PlanToken); err != nil {
		return nil, err
	}
	mutation, err := mutate(ctx, library, options.Revision, func(tx *sql.Tx, current State) (string, bool, error) {
		checked, missing, err := reconciliation(ctx, library, current, options)
		if err != nil {
			return "", false, err
		}
		if err := checkReconciliationToken(options.PlanToken, checked.PlanToken); err != nil {
			return "", false, err
		}
		for _, a := range missing {
			if _, err := tx.ExecContext(ctx, "DELETE FROM Assignment WHERE originUUID=? AND originTrackId=? AND valueId=?", a.OriginUUID, a.OriginTrackID, a.ValueID); err != nil {
				return "", false, err
			}
		}
		return "", len(missing) > 0, nil
	})
	if err != nil {
		return nil, err
	}
	result.Revision, result.Applied = mutation.Revision, true
	return result, nil
}
