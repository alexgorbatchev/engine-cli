package colors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

type ReconcileOptions struct {
	Revision  string `json:"revision"`
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token,omitempty"`
}

type Reconciliation struct {
	Revision           string `json:"revision"`
	PlanToken          string `json:"plan_token"`
	UnavailableTracks  int    `json:"unavailable_tracks"`
	RemovedAssignments int    `json:"removed_assignments"`
	Applied            bool   `json:"applied"`
}

func reconciliation(ctx context.Context, library string, state State, options ReconcileOptions) (*Reconciliation, State, error) {
	if err := checkRevision(options.Revision, state); err != nil {
		return nil, State{}, err
	}
	present, err := companion.Origins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, State{}, err
	}
	next := State{Assignments: []Assignment{}}
	missing := []Assignment{}
	for _, assignment := range canonical(state).Assignments {
		if present[companion.Origin{UUID: assignment.OriginUUID, ID: assignment.OriginTrackID}] {
			next.Assignments = append(next.Assignments, assignment)
		} else {
			missing = append(missing, assignment)
		}
	}
	// Equal counts are insufficient: Desktop may replace one unavailable origin
	// with another without changing any companion bytes.
	data, err := json.Marshal(struct {
		Revision string
		Missing  []Assignment
	}{state.Revision(), missing})
	if err != nil {
		return nil, State{}, err
	}
	sum := sha256.Sum256(data)
	result := &Reconciliation{Revision: state.Revision(), PlanToken: hex.EncodeToString(sum[:]), UnavailableTracks: len(missing), RemovedAssignments: len(missing)}
	return result, next, nil
}

func checkPlan(expected, actual string) error {
	if expected == "" || expected != actual {
		return failure("color_plan_stale", "review the current unavailable-track colors before removing them")
	}
	return nil
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
	if err := checkPlan(options.PlanToken, result.PlanToken); err != nil {
		return err
	}
	if result.RemovedAssignments == 0 {
		return failure("color_no_changes", "there are no unavailable-track colors to remove")
	}
	return nil
}

// Reconcile is explicit consent to discard colors for unavailable origins.
// Ordinary reads and unrelated edits preserve those recoverable assignments.
func Reconcile(ctx context.Context, library string, options ReconcileOptions) (*Reconciliation, error) {
	state, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	result, next, err := reconciliation(ctx, library, state, options)
	if err != nil {
		return nil, err
	}
	if options.DryRun {
		return result, nil
	}
	if err := checkPlan(options.PlanToken, result.PlanToken); err != nil {
		return nil, err
	}
	stored, err := replaceChecked(ctx, library, options.Revision, next, func(current State) error {
		checked, _, err := reconciliation(ctx, library, current, options)
		if err != nil {
			return err
		}
		return checkPlan(options.PlanToken, checked.PlanToken)
	})
	if err != nil {
		return nil, err
	}
	result.Revision, result.Applied = stored.Revision(), true
	return result, nil
}
