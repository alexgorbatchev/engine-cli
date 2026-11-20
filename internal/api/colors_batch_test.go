package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func colorBatchAPI(t *testing.T) (*Server, string) {
	t.Helper()
	s, library := testAPI(t)
	engine, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Exec("INSERT INTO Track(id,title,path,originDatabaseUuid,originTrackId) VALUES(2,'Second','second.mp3','other-origin',2),(3,'Third','third.mp3','other-origin',3)"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	return s, library
}

func TestTrackColorBatchReviewsEachTrackAndAppliesOnlyThePlan(t *testing.T) {
	s, library := colorBatchAPI(t)
	initial := runAPIJob(t, s, "track.color.inspect", `{"track":"2"}`)["result"].(map[string]any)
	seeded := runAPIJob(t, s, "track.color.update", string(mustJSON(t, map[string]any{"track": "2", "revision": initial["revision"], "color": "#FF0000"})))["result"].(map[string]any)
	review := map[string]any{"tracks": []string{"1", "2", "3", "1"}, "revision": seeded["revision"], "color": "#ffa500", "dry_run": true}
	plan := runAPIJob(t, s, "track.color.batch-update", string(mustJSON(t, review)))["result"].(map[string]any)
	if plan["applied"] != false || plan["changed_tracks"] != float64(3) || plan["cleared_tracks"] != float64(0) || plan["color"] != "#FFA500" {
		t.Fatalf("plan=%+v", plan)
	}
	rows := plan["tracks"].([]any)
	if len(rows) != 3 || rows[1].(map[string]any)["before"] != "#FF0000" || rows[1].(map[string]any)["after"] != "#FFA500" {
		t.Fatalf("each track's change=%+v", rows)
	}
	if after, _ := colors.Read(t.Context(), library); after.Revision() != seeded["revision"] {
		t.Fatalf("dry run changed colors: %+v", after)
	}
	review["dry_run"] = false
	review["plan_token"] = plan["plan_token"]
	applied := runAPIJob(t, s, "track.color.batch-update", string(mustJSON(t, review)))
	result := applied["result"].(map[string]any)
	if result["applied"] != true || result["revision"] == seeded["revision"] || applied["history_id"] == nil {
		t.Fatalf("apply=%+v", applied)
	}
	listed := runAPIJob(t, s, "track.color.list", `{}`)["result"].(map[string]any)
	if got := len(listed["tracks"].([]any)); got != 3 {
		t.Fatalf("colored tracks=%d %+v", got, listed)
	}
	// One undo restores every track's earlier color.
	if _, err := history.Undo(t.Context(), library, applied["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := colors.Read(t.Context(), library)
	if err != nil || restored.Revision() != seeded["revision"] {
		t.Fatalf("undo=%+v error=%v", restored, err)
	}
	clear := map[string]any{"tracks": []string{"1", "2"}, "revision": seeded["revision"], "color": "", "dry_run": true}
	cleared := runAPIJob(t, s, "track.color.batch-update", string(mustJSON(t, clear)))["result"].(map[string]any)
	if cleared["changed_tracks"] != float64(1) || cleared["cleared_tracks"] != float64(1) || cleared["color"] != "" {
		t.Fatalf("clear review=%+v", cleared)
	}
}

func TestRejectedTrackColorBatchesWriteNothing(t *testing.T) {
	s, library := colorBatchAPI(t)
	state, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.Revision()
	plan := runAPIJob(t, s, "track.color.batch-update", string(mustJSON(t, map[string]any{"tracks": []string{"1", "2"}, "revision": revision, "color": "#123456", "dry_run": true})))["result"].(map[string]any)
	tooMany := make([]string, colors.MaxBatchTracks+1)
	for i := range tooMany {
		tooMany[i] = "1"
	}
	for _, test := range []struct {
		name   string
		params map[string]any
		code   string
	}{
		{"no tracks", map[string]any{"tracks": []string{}, "revision": revision, "color": "#123456", "dry_run": true}, "operation_failed"},
		{"too many tracks", map[string]any{"tracks": tooMany, "revision": revision, "color": "#123456", "dry_run": true}, "color_tracks_invalid"},
		{"blank selector", map[string]any{"tracks": []string{""}, "revision": revision, "color": "#123456", "dry_run": true}, "operation_failed"},
		{"unknown track", map[string]any{"tracks": []string{"999"}, "revision": revision, "color": "#123456", "dry_run": true}, "track_not_found"},
		{"invalid color", map[string]any{"tracks": []string{"1"}, "revision": revision, "color": "red", "dry_run": true}, "color_invalid"},
		{"stale revision", map[string]any{"tracks": []string{"1"}, "revision": "stale", "color": "#123456", "dry_run": true}, "color_revision_stale"},
		{"no review", map[string]any{"tracks": []string{"1", "2"}, "revision": revision, "color": "#123456"}, "color_plan_stale"},
		{"other selection", map[string]any{"tracks": []string{"1"}, "revision": revision, "color": "#123456", "plan_token": plan["plan_token"]}, "color_plan_stale"},
		{"other color", map[string]any{"tracks": []string{"1", "2"}, "revision": revision, "color": "#654321", "plan_token": plan["plan_token"]}, "color_plan_stale"},
		{"nothing to clear", map[string]any{"tracks": []string{"1"}, "revision": revision, "color": "", "plan_token": "unused"}, "color_plan_stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, id, err := s.RunOperation(t.Context(), "track.color.batch-update", mustJSON(t, test.params))
			if progress.ErrorCode(err) != test.code || id != "" {
				t.Fatalf("rejection: code=%s history=%q error=%v", progress.ErrorCode(err), id, err)
			}
			if _, err := os.Stat(filepath.Join(library, ".engine-cli", "history")); !os.IsNotExist(err) {
				t.Fatalf("rejection recorded history: %v", err)
			}
		})
	}
	empty := map[string]any{"tracks": []string{"1"}, "revision": revision, "color": "", "dry_run": true}
	noChange := runAPIJob(t, s, "track.color.batch-update", string(mustJSON(t, empty)))["result"].(map[string]any)
	empty["dry_run"] = false
	empty["plan_token"] = noChange["plan_token"]
	if _, _, err := s.RunOperation(t.Context(), "track.color.batch-update", mustJSON(t, empty)); progress.ErrorCode(err) != "color_no_changes" {
		t.Fatalf("no-change apply error=%v", err)
	}
}
