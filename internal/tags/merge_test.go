package tags

import (
	"slices"
	"testing"
)

func TestMergingAValueMovesItsTracksAndDeletesIt(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	_, values, rev := scale(t, library, "Vibe", "Hypnotic", "Hypnotc")
	hypnotic, typo := values[0], values[1]
	_, others, rev := scaleAt(t, library, rev, "Energy", "Low")
	assigned, err := Assign(ctx, engine, library, AssignmentEdit{Track: "1", Values: []string{typo}, Revision: rev})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err = Assign(ctx, engine, library, AssignmentEdit{Track: "2", Values: []string{hypnotic, typo}, Revision: assigned.Revision})
	if err != nil {
		t.Fatal(err)
	}
	options := MergeOptions{ID: typo, Into: hypnotic, Revision: assigned.Revision, DryRun: true}
	if err := CheckMerge(ctx, library, options); err != nil {
		t.Fatal(err)
	}
	plan, err := MergeValue(ctx, library, options)
	if err != nil || plan.Applied || plan.AffectedTracks != 2 || plan.MovedAssignments != 1 || plan.PlanToken == "" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	// Merging needs the reviewed plan, and only within one category.
	options.DryRun = false
	requireCode(t, CheckMerge(ctx, library, options), "tag_plan_stale")
	_, err = MergeValue(ctx, library, options)
	requireCode(t, err, "tag_plan_stale")
	requireCode(t, CheckMerge(ctx, library, MergeOptions{ID: typo, Into: others[0], Revision: assigned.Revision, DryRun: true}), "tag_merge_invalid")
	requireCode(t, CheckMerge(ctx, library, MergeOptions{ID: typo, Into: typo, Revision: assigned.Revision, DryRun: true}), "tag_merge_invalid")
	requireCode(t, CheckMerge(ctx, library, MergeOptions{ID: "missing", Into: hypnotic, Revision: assigned.Revision, DryRun: true}), "tag_not_found")
	requireCode(t, CheckMerge(ctx, library, MergeOptions{ID: typo, Into: hypnotic, Revision: "stale", DryRun: true}), "tag_revision_stale")
	options.PlanToken = plan.PlanToken
	if err := CheckMerge(ctx, library, options); err != nil {
		t.Fatal(err)
	}
	applied, err := MergeValue(ctx, library, options)
	if err != nil || !applied.Applied || applied.Revision == assigned.Revision {
		t.Fatalf("applied = %+v, %v", applied, err)
	}
	if _, listed := names(t, library); !slices.Equal(listed, []string{"Hypnotic", "Low"}) {
		t.Fatalf("values after merge = %v", listed)
	}
	for _, track := range []string{"1", "2"} {
		held, err := InspectTrack(ctx, engine, library, track)
		if err != nil || !slices.Equal(held.Values, []string{hypnotic}) {
			t.Fatalf("track %s holds %+v, %v", track, held, err)
		}
	}
}

// scaleAt creates a category with values starting from a known revision.
func scaleAt(t *testing.T, library, rev, name string, values ...string) (string, []string, string) {
	t.Helper()
	ctx := t.Context()
	c, err := EditCategory(ctx, library, Edit{Name: name, Revision: rev})
	if err != nil {
		t.Fatal(err)
	}
	ids, latest := []string{}, c.Revision
	for _, value := range values {
		v, err := EditValue(ctx, library, Edit{Category: c.ID, Name: value, Revision: latest})
		if err != nil {
			t.Fatal(err)
		}
		ids, latest = append(ids, v.ID), v.Revision
	}
	return c.ID, ids, latest
}
