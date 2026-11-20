package colors

import (
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestColorBatchRefusesEmptyOrOversizedSelections(t *testing.T) {
	for _, selectors := range [][]string{nil, {" "}, make([]string, MaxBatchTracks+1)} {
		if _, err := resolveBatch(t.Context(), nil, selectors); progress.ErrorCode(err) != "color_tracks_invalid" {
			t.Fatalf("selectors=%d error=%v", len(selectors), err)
		}
	}
}

func TestColorBatchPlanBindsSelectionColorAndRevision(t *testing.T) {
	first := companion.Track{ID: 1, Origin: companion.Origin{UUID: "library-a", ID: 1}}
	second := companion.Track{ID: 2, Origin: companion.Origin{UUID: "library-a", ID: 2}}
	state := State{Assignments: []Assignment{{OriginUUID: "library-a", OriginTrackID: 2, Color: "#FF0000"}}}
	options := BatchOptions{Color: "#00ff00", Revision: state.Revision()}
	plan, next, err := batchPlan(state, []companion.Track{first, second}, options)
	if err != nil || plan.ChangedTracks != 2 || plan.Color != "#00FF00" || len(next.Assignments) != 2 {
		t.Fatalf("plan=%+v next=%+v error=%v", plan, next, err)
	}
	other, _, err := batchPlan(state, []companion.Track{first}, options)
	if err != nil || other.PlanToken == plan.PlanToken {
		t.Fatalf("a narrower selection kept the plan token: %v", err)
	}
	cleared, next, err := batchPlan(state, []companion.Track{first, second}, BatchOptions{Revision: state.Revision()})
	if err != nil || cleared.ChangedTracks != 1 || cleared.ClearedTracks != 1 || len(next.Assignments) != 0 {
		t.Fatalf("clear=%+v next=%+v error=%v", cleared, next, err)
	}
	if _, _, err := batchPlan(state, []companion.Track{first}, BatchOptions{Color: "red", Revision: state.Revision()}); progress.ErrorCode(err) != "color_invalid" {
		t.Fatalf("invalid color error=%v", err)
	}
	if _, _, err := batchPlan(state, []companion.Track{first}, BatchOptions{Color: "#00FF00", Revision: "stale"}); progress.ErrorCode(err) != "color_revision_stale" {
		t.Fatalf("stale revision error=%v", err)
	}
}
