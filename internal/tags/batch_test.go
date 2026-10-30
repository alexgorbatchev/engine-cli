package tags

import (
	"context"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestBatchTransactionRollsBackEarlierAssignments(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	c, err := EditCategory(ctx, library, Edit{Name: "Energy", Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := EditValue(ctx, library, Edit{Category: c.ID, Name: "Warm", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(companion.TagsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TRIGGER reject_second_assignment BEFORE INSERT ON Assignment WHEN NEW.originTrackId=20 BEGIN SELECT RAISE(ABORT,'injected assignment failure'); END`); err != nil {
		t.Fatal(err)
	}
	options := BatchOptions{Tracks: []string{"1", "2"}, Revision: v.Revision, Add: []string{v.ID}, Remove: []string{}, DryRun: true}
	preview, err := AssignBatch(ctx, engine, library, options)
	if err != nil {
		t.Fatal(err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	if _, err := AssignBatch(ctx, engine, library, options); err == nil {
		t.Fatal("failing second assignment reported success")
	}
	state, err := Read(ctx, library)
	if err != nil || len(state.Assignments) != 0 || state.Revision() != v.Revision {
		t.Fatalf("failed transaction retained partial assignments: %+v error=%v", state, err)
	}
}

func TestBatchCountsResolvedSelectionAndRejectsInvalidSelectors(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	c, err := EditCategory(ctx, library, Edit{Name: "Energy", Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := EditValue(ctx, library, Edit{Category: c.ID, Name: "Warm", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	options := BatchOptions{Tracks: []string{"2", "1", "2"}, Revision: v.Revision, Add: []string{v.ID}, Remove: []string{}, DryRun: true}
	preview, err := AssignBatch(ctx, engine, library, options)
	if err != nil || preview.ChangedTracks != 2 || preview.AddedAssignments != 2 || !slices.Equal(preview.TrackIDs, []int64{2, 1}) {
		t.Fatalf("resolved selection preview=%+v error=%v", preview, err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	if _, err := AssignBatch(ctx, engine, library, options); err != nil {
		t.Fatal(err)
	}
	inspected, err := InspectBatch(ctx, engine, library, options.Tracks)
	if err != nil || inspected.Categories[0].Values[0].SelectedTracks != 2 {
		t.Fatalf("selected assignment counts=%+v error=%v", inspected, err)
	}
	for _, selectors := range [][]string{nil, {" "}, make([]string, maxBatchTracks+1)} {
		_, err := InspectBatch(ctx, engine, library, selectors)
		requireCode(t, err, "tag_tracks_invalid")
	}
}

func TestBatchMixedKeepAddRemoveAndDeduplication(t *testing.T) {
	ctx := context.Background()
	library, engine := tagLibrary(t)
	c, err := EditCategory(ctx, library, Edit{Name: "Energy", Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := EditValue(ctx, library, Edit{Category: c.ID, Name: "Warm", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	driving, err := EditValue(ctx, library, Edit{Category: c.ID, Name: "Driving", Revision: warm.Revision})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Assign(ctx, engine, library, AssignmentEdit{Track: "1", Values: []string{warm.ID}, Revision: driving.Revision})
	if err != nil {
		t.Fatal(err)
	}
	selection := []string{"2", "1", "2"}
	inspected, err := InspectBatch(ctx, engine, library, selection)
	if err != nil || !slices.Equal(inspected.TrackIDs, []int64{2, 1}) {
		t.Fatalf("inspect=%+v error=%v", inspected, err)
	}
	for _, value := range inspected.Categories[0].Values {
		if value.ID == warm.ID && value.SelectedTracks != 1 {
			t.Fatalf("mixed value=%+v", value)
		}
	}
	options := BatchOptions{Tracks: selection, Revision: inspected.Revision, Add: []string{driving.ID}, Remove: []string{}, DryRun: true}
	preview, err := AssignBatch(ctx, engine, library, options)
	if err != nil || preview.ChangedTracks != 2 || preview.AddedAssignments != 2 || preview.RemovedAssignments != 0 || preview.Applied {
		t.Fatalf("preview=%+v error=%v", preview, err)
	}
	before, err := InspectTrack(ctx, engine, library, "1")
	if err != nil || !slices.Equal(before.Values, []string{warm.ID}) {
		t.Fatalf("preview changed assignments: %+v %v", before, err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	applied, err := AssignBatch(ctx, engine, library, options)
	if err != nil || !applied.Applied || applied.Revision == preview.Revision {
		t.Fatalf("apply=%+v error=%v", applied, err)
	}
	first, err := InspectTrack(ctx, engine, library, "1")
	if err != nil || len(first.Values) != 2 || !slices.Contains(first.Values, warm.ID) || !slices.Contains(first.Values, driving.ID) {
		t.Fatalf("keep=%+v error=%v", first, err)
	}
	options.Revision, options.DryRun = applied.Revision, true
	options.Add, options.Remove = []string{}, []string{warm.ID}
	preview, err = AssignBatch(ctx, engine, library, options)
	if err != nil || preview.ChangedTracks != 1 || preview.RemovedAssignments != 1 {
		t.Fatalf("remove preview=%+v error=%v", preview, err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	applied, err = AssignBatch(ctx, engine, library, options)
	if err != nil {
		t.Fatal(err)
	}
	options.Revision, options.DryRun = applied.Revision, true
	preview, err = AssignBatch(ctx, engine, library, options)
	if err != nil || preview.ChangedTracks != 0 {
		t.Fatalf("noop=%+v error=%v", preview, err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	noop, err := AssignBatch(ctx, engine, library, options)
	if progress.ErrorCode(err) != "tag_no_changes" || noop != nil {
		t.Fatalf("noop apply=%+v error=%v", noop, err)
	}
}

func TestBatchRejectsUnreviewedActionsAndNativeIdentityChanges(t *testing.T) {
	ctx := context.Background()
	library, engine := tagLibrary(t)
	c, err := EditCategory(ctx, library, Edit{Name: "Energy", Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := EditValue(ctx, library, Edit{Category: c.ID, Name: "Warm", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	options := BatchOptions{Tracks: []string{"1", "2"}, Revision: v.Revision, Add: []string{v.ID}, Remove: []string{}, DryRun: true}
	preview, err := AssignBatch(ctx, engine, library, options)
	if err != nil {
		t.Fatal(err)
	}
	options.DryRun = false
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_plan_stale")
	options.PlanToken = preview.PlanToken
	if _, err := engine.Exec("UPDATE Track SET originTrackId=30 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_plan_stale")
	state, err := Read(ctx, library)
	if err != nil || len(state.Assignments) != 0 {
		t.Fatalf("partial mutation: %+v %v", state, err)
	}
	options.DryRun = true
	options.Remove = []string{v.ID}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_actions_overlap")
	options.Remove, options.Add = []string{}, []string{v.ID, v.ID}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_value_duplicate")
	options.Add = nil
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_values_required")
	options.Add = []string{"missing-value"}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_not_found")
	options.Add, options.Tracks = []string{v.ID}, []string{"1", "missing"}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "track_not_found")
	options.Tracks = []string{}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_tracks_invalid")
	options.Tracks = []string{"1"}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := AssignBatch(cancelled, engine, library, options); err == nil {
		t.Fatal("cancelled batch succeeded")
	}
}
