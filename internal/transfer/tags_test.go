package transfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func transferTags(t *testing.T, lib string) tags.State {
	t.Helper()
	s, err := tags.Read(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func seedTransferTags(t *testing.T, source string) {
	t.Helper()
	state := tags.State{Categories: []tags.Category{
		{ID: "mood", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}, {ID: "cool", Name: "Cool"}}},
		{ID: "unused", Name: "Unused", Values: []tags.Value{{ID: "other", Name: "Other"}}},
	}, Assignments: []tags.Assignment{{OriginUUID: "source", OriginTrackID: 1, ValueID: "warm"}, {OriginUUID: "source", OriginTrackID: 2, ValueID: "cool"}}}
	if _, err := tags.Replace(context.Background(), source, transferTags(t, source).Revision(), state); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCompanionPublicationFailureRetainsRecoverableHistory(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	seedTransferTags(t, source)
	if _, err := tags.EditCategory(t.Context(), destination, tags.Edit{Name: "Local", Revision: transferTags(t, destination).Revision()}); err != nil {
		t.Fatal(err)
	}
	before := transferTags(t, destination)
	companionDB, err := db.Open(companion.TagsPath(destination))
	if err != nil {
		t.Fatal(err)
	}
	defer companionDB.Close()
	// A real SQLite publication failure after Engine commits, without replacing
	// production functions or pretending independent files share a transaction.
	if _, err := companionDB.Exec("CREATE TRIGGER reject_publication BEFORE INSERT ON Category BEGIN SELECT RAISE(ABORT,'synthetic publication failure'); END"); err != nil {
		t.Fatal(err)
	}
	_, err = Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true})
	if progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("cross-file failure did not preserve pending recovery: %v", err)
	}
	var tracks int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&tracks); err != nil || tracks != 2 {
		t.Fatalf("test did not reach committed Engine half: tracks=%d error=%v", tracks, err)
	}
	if got := transferTags(t, destination); got.Revision() != before.Revision() {
		t.Fatalf("failed companion transaction changed taxonomy: %+v", got)
	}
	entries, err := history.List(destination)
	if err != nil || len(entries) != 1 || entries[0].After != nil {
		t.Fatalf("pending history=%+v error=%v", entries, err)
	}
	if _, err := history.Undo(t.Context(), destination, entries[0].ID, history.UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&tracks); err != nil || tracks != 0 {
		t.Fatalf("recovery left Engine half: tracks=%d error=%v", tracks, err)
	}
	if got := transferTags(t, destination); got.Revision() != before.Revision() {
		t.Fatalf("recovery lost original companion: %+v", got)
	}
}

func TestSyncCustomTagsScopeAndUndo(t *testing.T) {
	for _, root := range []bool{false, true} {
		t.Run(map[bool]string{false: "playlist", true: "root"}[root], func(t *testing.T) {
			source, s := library(t, "source")
			destination, _ := library(t, "destination")
			execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3'); INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0)`)
			for _, name := range []string{"one.mp3", "two.mp3"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			seedTransferTags(t, source)
			o := Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{1}}, DryRun: true, NoBackup: true}
			if root {
				o.Scope = Scope{Root: true}
			}
			preview, err := Run(context.Background(), o)
			if err != nil || !preview.Applicable {
				t.Fatalf("preview=%+v %v", preview, err)
			}
			if _, err := os.Stat(companion.TagsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("preview enrolled destination: %v", err)
			}
			o.DryRun = false
			o.PlanToken = preview.PlanToken
			applied, err := Run(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			state := transferTags(t, destination)
			if root {
				if len(state.Categories) != 2 || len(state.Assignments) != 2 {
					t.Fatalf("root omitted taxonomy: %+v", state)
				}
			} else if len(state.Categories) != 1 || len(state.Categories[0].Values) != 1 || len(state.Assignments) != 1 || state.Assignments[0].OriginUUID != "source" || state.Assignments[0].OriginTrackID != 1 {
				t.Fatalf("playlist scope leaked taxonomy or lost native identity: %+v", state)
			}
			if _, err := history.Undo(context.Background(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(companion.TagsPath(destination)); !os.IsNotExist(err) {
				t.Fatalf("undo retained newly enrolled taxonomy: %v", err)
			}
		})
	}
}

func TestSyncTagRevisionInvalidatesExactPlan(t *testing.T) {
	source, s := library(t, "source")
	destination, _ := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	seedTransferTags(t, source)
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tags.Assign(context.Background(), s, source, tags.AssignmentEdit{Track: "1", Revision: transferTags(t, source).Revision(), Values: []string{"cool"}}); err != nil {
		t.Fatal(err)
	}
	o.DryRun = false
	o.PlanToken = preview.PlanToken
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("accepted a plan after assignments changed")
	}
	if _, err := os.Stat(companion.TagsPath(destination)); !os.IsNotExist(err) {
		t.Fatalf("stale plan changed destination tags: %v", err)
	}
}

func TestSyncDistinctTaxonomyNameCollisionIsConflict(t *testing.T) {
	source, _ := library(t, "source")
	destination, _ := library(t, "destination")
	for _, lib := range []string{source, destination} {
		if _, err := tags.EditCategory(context.Background(), lib, tags.Edit{Name: "Mood", Revision: transferTags(t, lib).Revision()}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Run(context.Background(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, Conflict: "source"})
	if err != nil || report.Applicable {
		t.Fatalf("same-name identities silently collapsed: %+v %v", report, err)
	}
	found := false
	for _, conflict := range report.Conflicts {
		if conflict.Code == "sync_tag_name_conflict" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing taxonomy conflict: %+v", report.Conflicts)
	}
}

func TestSyncUnavailableAssignmentsRequireExplicitReconciliation(t *testing.T) {
	source, s := library(t, "source")
	destination, _ := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	seedTransferTags(t, source)
	before := transferTags(t, source)
	execSQL(t, s, "DELETE FROM Track WHERE id=1")
	_, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true})
	if progress.ErrorCode(err) != "tag_assignments_stale" {
		t.Fatalf("unavailable assignments did not direct reviewed reconciliation: %v", err)
	}
	if after := transferTags(t, source); after.Revision() != before.Revision() {
		t.Fatal("sync preview silently pruned unavailable assignments")
	}
	if _, err := os.Stat(companion.TagsPath(destination)); !os.IsNotExist(err) {
		t.Fatalf("rejected preview enrolled destination: %v", err)
	}
}

func TestCustomTagsReverseSyncAndSubsequentForward(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3'),('Two','two.mp3','two.mp3')`)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		put(t, filepath.Join(source, name), name)
	}
	seedTransferTags(t, source)
	forward := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(t.Context(), forward); err != nil {
		t.Fatal(err)
	}
	if _, err := tags.Assign(t.Context(), d, destination, tags.AssignmentEdit{Track: "1", Revision: transferTags(t, destination).Revision(), Values: []string{"cool", "warm"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tags.EditCategory(t.Context(), destination, tags.Edit{ID: "mood", Name: "Energy", Revision: transferTags(t, destination).Revision()}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), Options{Source: destination, Destination: source, Scope: Scope{Root: true}, NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := transferTags(t, source).Revision(), transferTags(t, destination).Revision(); got != want {
		t.Fatalf("reverse sync lost taxonomy or assignments: source=%s destination=%s", got, want)
	}
	if _, err := tags.Assign(t.Context(), s, source, tags.AssignmentEdit{Track: "1", Revision: transferTags(t, source).Revision(), Values: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), forward); err != nil {
		t.Fatal(err)
	}
	if got, want := transferTags(t, destination).Revision(), transferTags(t, source).Revision(); got != want {
		t.Fatalf("forward sync after reverse lost cleared assignments: destination=%s source=%s", got, want)
	}
	unchanged, err := Run(t.Context(), forward)
	if err != nil || unchanged.OperationID != "" {
		t.Fatalf("unchanged tags created another operation: %+v error=%v", unchanged, err)
	}
}

func TestRootMirrorControlsDestinationOnlyTaxonomy(t *testing.T) {
	source, _ := library(t, "source")
	destination, _ := library(t, "destination")
	if _, err := tags.EditCategory(t.Context(), destination, tags.Edit{Name: "Local", Revision: transferTags(t, destination).Revision()}); err != nil {
		t.Fatal(err)
	}
	before := transferTags(t, destination)
	options := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	if after := transferTags(t, destination); after.Revision() != before.Revision() {
		t.Fatal("ordinary sync removed destination-only taxonomy")
	}
	options.Mirror, options.DryRun = true, true
	preview, err := Run(t.Context(), options)
	if err != nil || !preview.Applicable {
		t.Fatalf("mirror preview=%+v error=%v", preview, err)
	}
	if after := transferTags(t, destination); after.Revision() != before.Revision() {
		t.Fatal("mirror preview removed taxonomy")
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	applied, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if after := transferTags(t, destination); len(after.Categories) != 0 {
		t.Fatalf("explicit root mirror retained destination-only taxonomy: %+v", after)
	}
	if _, err := history.Undo(t.Context(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := transferTags(t, destination); after.Revision() != before.Revision() {
		t.Fatal("undo did not restore destination-only taxonomy")
	}
}
