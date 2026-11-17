package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func undoReview(t *testing.T, job map[string]any) history.UndoReview {
	t.Helper()
	raw, err := json.Marshal(job["result"])
	if err != nil {
		t.Fatal(err)
	}
	var review history.UndoReview
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatalf("result is not an undo review: %s", raw)
	}
	return review
}

func reviewRequest(t *testing.T, fields map[string]any) string {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func trackGenre(t *testing.T, lib string, id int) string {
	t.Helper()
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var genre string
	if err := database.QueryRowContext(context.Background(), "SELECT COALESCE(genre,'') FROM Track WHERE id=?", id).Scan(&genre); err != nil {
		t.Fatal(err)
	}
	return genre
}

func expectUndoCode(t *testing.T, s *Server, body, code string) {
	t.Helper()
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "library.undo", body))
	failure, _ := job["error"].(map[string]any)
	if job["state"] != "failed" || failure["code"] != code {
		t.Fatalf("expected %s: %+v", code, job)
	}
}

func TestAPIUndoReviewListsCurrentAndRestoredValuesAndAppliesItsPlan(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	id := historyIdentity(t, runAPIJob(t, s, "track.batch.update", `{"identifiers":["1","2"],"genre":"House"}`))
	review := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true})))
	if len(review.Records) != 1 || review.Records[0].ID != id || review.Records[0].Summary == nil {
		t.Fatalf("records=%+v", review.Records)
	}
	if review.Updates.Tracks != 2 || len(review.Changes) != 2 || len(review.Conflicts) != 0 || review.PlanToken == "" || !review.Coverage.Redo {
		t.Fatalf("review=%+v", review)
	}
	byTitle := map[string]history.ReviewChange{}
	for _, change := range review.Changes {
		byTitle[change.Title] = change
	}
	ritual := byTitle["Ritual Shadows"]
	if ritual.Action != "update" || ritual.TrackID != 2 || len(ritual.Fields) != 1 || ritual.Fields[0].Field != "genre" || ritual.Fields[0].From != "House" || ritual.Fields[0].To != "Techno" {
		t.Fatalf("ritual change=%+v", ritual)
	}
	if curated := byTitle["Curated title"]; len(curated.Fields) != 1 || curated.Fields[0].To != nil {
		t.Fatalf("curated change=%+v", curated)
	}
	if trackGenre(t, lib, 2) != "House" {
		t.Fatal("dry run changed the library")
	}
	undone := runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "plan_token": review.PlanToken}))
	if trackGenre(t, lib, 2) != "Techno" || trackGenre(t, lib, 1) != "" {
		t.Fatal("reviewed undo did not restore the values")
	}
	status := runAPIJob(t, s, "library.undo.status", reviewRequest(t, map[string]any{"plan_token": review.PlanToken}))
	result := status["result"].(map[string]any)
	if result["state"] != "applied" || result["history_id"] != historyIdentity(t, undone) {
		t.Fatalf("status=%+v", status)
	}
	status = runAPIJob(t, s, "library.undo.status", `{"plan_token":"unknown"}`)
	if status["result"].(map[string]any)["state"] != "not_applied" {
		t.Fatalf("unknown status=%+v", status)
	}
}

func TestAPIUndoReviewRefusesAStalePlan(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	id := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"2","genre":"House"}`))
	review := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true})))
	runAPIJob(t, s, "track.update", `{"identifier":"1","comment":"later"}`)
	expectUndoCode(t, s, reviewRequest(t, map[string]any{"id": id, "plan_token": review.PlanToken}), "undo_plan_stale")
	if trackGenre(t, lib, 2) != "House" {
		t.Fatal("stale plan changed the library")
	}
	if w := requestAPI(t, s, "POST", "/v1/operations/library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true, "plan_token": "x"})); w.Code != 400 {
		t.Fatalf("dry run accepted a plan token: %d", w.Code)
	}
}

func TestAPIUndoReviewNamesLaterEditsAndKeepsThem(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	id := historyIdentity(t, runAPIJob(t, s, "track.batch.update", `{"identifiers":["1","2"],"genre":"House"}`))
	later := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"2","genre":"Deep House"}`))
	review := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true})))
	if len(review.Conflicts) != 1 || review.Blocked {
		t.Fatalf("conflicts=%+v", review.Conflicts)
	}
	conflict := review.Conflicts[0]
	if conflict.Title != "Ritual Shadows" || conflict.TrackID != 2 || conflict.Later == nil || conflict.Later.ID != later || conflict.Later.Operation != "track.update" {
		t.Fatalf("conflict=%+v", conflict)
	}
	if len(conflict.Fields) != 1 || conflict.Fields[0].Field != "genre" || conflict.Fields[0].From != "House" || conflict.Fields[0].To != "Deep House" {
		t.Fatalf("conflict fields=%+v", conflict.Fields)
	}
	if len(review.Changes) != 1 || review.Changes[0].TrackID != 1 {
		t.Fatalf("kept changes=%+v", review.Changes)
	}
	// Without a review the undo still refuses the conflict.
	expectUndoCode(t, s, reviewRequest(t, map[string]any{"id": id}), "undo_conflict")
	runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "plan_token": review.PlanToken}))
	if trackGenre(t, lib, 2) != "Deep House" || trackGenre(t, lib, 1) != "" {
		t.Fatalf("keep later edits: %q %q", trackGenre(t, lib, 2), trackGenre(t, lib, 1))
	}
}

func TestAPIUndoReviewExcludesChosenRows(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	id := historyIdentity(t, runAPIJob(t, s, "track.batch.update", `{"identifiers":["1","2"],"genre":"House"}`))
	first := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true})))
	var keep string
	for _, change := range first.Changes {
		if change.TrackID == 2 {
			keep = change.Entity + "#genre"
		}
	}
	narrowed := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true, "exclude": []string{keep}})))
	if len(narrowed.Changes) != 1 || narrowed.Changes[0].TrackID != 1 || narrowed.PlanToken == first.PlanToken {
		t.Fatalf("narrowed=%+v", narrowed)
	}
	// The token binds the exclusions: the narrowed token cannot apply the full plan.
	expectUndoCode(t, s, reviewRequest(t, map[string]any{"id": id, "plan_token": narrowed.PlanToken}), "undo_plan_stale")
	runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "exclude": []string{keep}, "plan_token": narrowed.PlanToken}))
	if trackGenre(t, lib, 2) != "House" || trackGenre(t, lib, 1) != "" {
		t.Fatalf("exclusion: %q %q", trackGenre(t, lib, 2), trackGenre(t, lib, 1))
	}
	if w := requestAPI(t, s, "POST", "/v1/operations/library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true, "exclude": []string{"Track"}})); w.Code != 400 {
		t.Fatalf("invalid exclude accepted: %d %s", w.Code, w.Body)
	}
}

func TestAPIUndoSeveralRecordsTogether(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	first := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"1","genre":"House","group_id":"run"}`))
	second := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"2","genre":"Disco","group_id":"run"}`))
	review := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"ids": []string{first, second}, "dry_run": true})))
	if len(review.Records) != 2 || review.Records[0].ID != second || len(review.Changes) != 2 {
		t.Fatalf("review=%+v", review)
	}
	undone := historyIdentity(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"ids": []string{first, second}, "plan_token": review.PlanToken})))
	if trackGenre(t, lib, 1) != "" || trackGenre(t, lib, 2) != "Techno" {
		t.Fatal("several records were not undone together")
	}
	for _, id := range []string{first, second} {
		if entry := historyEntry(t, s, id); entry["undone_by"] != undone {
			t.Fatalf("entry %s=%+v", id, entry)
		}
	}
	expectUndoCode(t, s, reviewRequest(t, map[string]any{"ids": []string{first, second}, "dry_run": true}), "operation_failed")
	runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": undone}))
	if trackGenre(t, lib, 1) != "House" || trackGenre(t, lib, 2) != "Disco" {
		t.Fatal("redo of a multi-record undo did not restore both records")
	}
}

func TestAPIUndoReviewReportsRemovalsAndRunsWhileDesktopRuns(t *testing.T) {
	s, _ := testAPI(t)
	id := historyIdentity(t, runAPIJob(t, s, "playlist.track.add", `{"playlist":"1","tracks":["1"]}`))
	created := historyIdentity(t, runAPIJob(t, s, "playlist.create", `{"name":"Warmup"}`))
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	membership := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": id, "dry_run": true})))
	if membership.Removes.Memberships != 1 || len(membership.Changes) != 1 || membership.Changes[0].Playlist != "Favorites" || membership.Changes[0].Title != "Curated title" || membership.Changes[0].Action != "remove" {
		t.Fatalf("membership review=%+v", membership)
	}
	playlist := undoReview(t, runAPIJob(t, s, "library.undo", reviewRequest(t, map[string]any{"id": created, "dry_run": true})))
	if playlist.Removes.Playlists != 1 || playlist.Changes[0].Title != "Warmup" {
		t.Fatalf("playlist review=%+v", playlist)
	}
	expectUndoCode(t, s, reviewRequest(t, map[string]any{"id": created}), "engine_desktop_running")
}
