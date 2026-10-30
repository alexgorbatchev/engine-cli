package tags

import (
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func TestEmptyReconciliationDoesNotEnrollCompanion(t *testing.T) {
	library, _ := tagLibrary(t)
	stored := currentRevision(t, library)
	preview, err := Reconcile(t.Context(), library, ReconcileOptions{Revision: stored, DryRun: true})
	if err != nil || preview.Applied || preview.UnavailableTracks != 0 || preview.RemovedAssignments != 0 {
		t.Fatalf("empty preview=%+v error=%v", preview, err)
	}
	applied, err := Reconcile(t.Context(), library, ReconcileOptions{Revision: stored, PlanToken: preview.PlanToken})
	if err != nil || !applied.Applied || applied.Revision != stored || applied.PlanToken != preview.PlanToken {
		t.Fatalf("empty apply=%+v error=%v", applied, err)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty reconciliation enrolled companion: %v", err)
	}
}

func TestDeletionReviewBindsLiveIdentitiesAndCountsUnavailableAssignments(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	state := State{Categories: []Category{{ID: "mood", Name: "Mood", Values: []Value{{ID: "warm", Name: "Warm"}}}}, Assignments: []Assignment{{OriginUUID: "library-a", OriginTrackID: 10, ValueID: "warm"}, {OriginUUID: "library-a", OriginTrackID: 20, ValueID: "warm"}}}
	if _, err := Replace(ctx, library, currentRevision(t, library), state); err != nil {
		t.Fatal(err)
	}
	stored := currentRevision(t, library)
	if _, err := engine.Exec("DELETE FROM Track WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	options := DeleteOptions{ID: "warm", Revision: stored, DryRun: true}
	preview, err := Delete(ctx, library, options, false)
	if err != nil || preview.AffectedTracks != 1 || preview.RemovedAssignments != 2 || preview.RemovedValues != 1 {
		t.Fatalf("deletion impact=%+v error=%v", preview, err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	// The count remains one, but the affected native identity has changed.
	if _, err := engine.Exec("UPDATE Track SET originTrackId=10 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	_, err = Delete(ctx, library, options, false)
	requireCode(t, err, "tag_plan_stale")
	if currentRevision(t, library) != stored {
		t.Fatal("stale deletion review changed stored assignments")
	}
	if _, err := engine.Exec("UPDATE Track SET originTrackId=20 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	applied, err := Delete(ctx, library, options, false)
	if err != nil || !applied.Applied || applied.AffectedTracks != 1 || applied.RemovedAssignments != 2 || applied.PlanToken != preview.PlanToken {
		t.Fatalf("applied impact=%+v error=%v", applied, err)
	}
	after, err := Read(ctx, library)
	if err != nil || len(after.Assignments) != 0 || len(after.Categories[0].Values) != 0 {
		t.Fatalf("reviewed deletion did not remove all stored pairs: %+v error=%v", after, err)
	}
}

func TestExternalTrackRemovalRequiresReviewedReconciliation(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	state := State{Categories: []Category{{ID: "mood", Name: "Mood", Values: []Value{{ID: "warm", Name: "Warm"}, {ID: "deep", Name: "Deep"}}}}, Assignments: []Assignment{{OriginUUID: "library-a", OriginTrackID: 10, ValueID: "warm"}, {OriginUUID: "library-a", OriginTrackID: 10, ValueID: "deep"}, {OriginUUID: "library-a", OriginTrackID: 20, ValueID: "warm"}}}
	if _, err := Replace(ctx, library, currentRevision(t, library), state); err != nil {
		t.Fatal(err)
	}
	stored := currentRevision(t, library)
	if _, err := engine.Exec("DELETE FROM Track WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	listed, err := List(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Revision != stored || listed.UnavailableTracks != 1 {
		t.Fatalf("external removal changed stored revision or wrong distinct impact: %+v", listed)
	}
	for _, v := range listed.Categories[0].Values {
		want := 0
		if v.ID == "warm" {
			want = 1
		}
		if v.TrackCount != want {
			t.Fatalf("live count=%+v want=%d", v, want)
		}
	}
	inspected, err := InspectTrack(ctx, engine, library, "2")
	if err != nil || inspected.Revision != stored {
		t.Fatalf("inspect revision diverged: %+v %v", inspected, err)
	}
	preview, err := Reconcile(ctx, library, ReconcileOptions{Revision: stored, DryRun: true})
	if err != nil || preview.UnavailableTracks != 1 || preview.RemovedAssignments != 2 || preview.Applied {
		t.Fatalf("preview=%+v error=%v", preview, err)
	}
	if currentRevision(t, library) != stored {
		t.Fatal("read or preview pruned stored assignments")
	}
	// Desktop can restore an origin while companion bytes remain unchanged.
	if _, err := engine.Exec("UPDATE Track SET originTrackId=10 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	_, err = Reconcile(ctx, library, ReconcileOptions{Revision: stored, PlanToken: preview.PlanToken})
	requireCode(t, err, "tag_plan_stale")
	if _, err := engine.Exec("UPDATE Track SET originTrackId=20 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	applied, err := Reconcile(ctx, library, ReconcileOptions{Revision: stored, PlanToken: preview.PlanToken})
	if err != nil || !applied.Applied || applied.PlanToken != preview.PlanToken || applied.RemovedAssignments != 2 || applied.UnavailableTracks != 1 || applied.Revision == stored {
		t.Fatalf("apply=%+v error=%v", applied, err)
	}
	after, err := Read(ctx, library)
	if err != nil || len(after.Assignments) != 1 || after.Assignments[0].OriginTrackID != 20 {
		t.Fatalf("reconciliation removed wrong identities: %+v %v", after, err)
	}
}
