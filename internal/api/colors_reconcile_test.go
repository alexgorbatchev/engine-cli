package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestTrackColorReconciliationReviewApplyAndUndo(t *testing.T) {
	s, library := testAPI(t)
	engine, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	initial, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	empty := runAPIJob(t, s, "track.color.reconcile", string(mustJSON(t, map[string]any{"revision": initial.Revision(), "dry_run": true})))
	if empty["history_id"] != nil || empty["result"].(map[string]any)["unavailable_tracks"] != float64(0) {
		t.Fatalf("empty review=%+v", empty)
	}
	if _, err := os.Stat(companion.ColorsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("empty review enrolled storage: %v", err)
	}
	emptyImpact := empty["result"].(map[string]any)
	result, id, err := s.RunOperation(t.Context(), "track.color.reconcile", mustJSON(t, map[string]any{"revision": initial.Revision(), "plan_token": emptyImpact["plan_token"]}))
	if result != nil || id != "" || progress.ErrorCode(err) != "color_no_changes" {
		t.Fatalf("empty apply=%v history=%q error=%v", result, id, err)
	}
	for _, name := range []string{"backups", "history"} {
		if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
			t.Fatalf("no-op apply enrolled %s: %v", name, err)
		}
	}
	assigned, err := colors.Assign(t.Context(), engine, library, colors.Edit{Track: "1", Color: "#123456", Revision: initial.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Exec("DELETE FROM Track WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	listed := runAPIJob(t, s, "track.color.list", `{}`)["result"].(map[string]any)
	if listed["revision"] != assigned.Revision || listed["unavailable_tracks"] != float64(1) || len(listed["tracks"].([]any)) != 0 {
		t.Fatalf("unavailable projection=%+v", listed)
	}
	preview := runAPIJob(t, s, "track.color.reconcile", string(mustJSON(t, map[string]any{"revision": assigned.Revision, "dry_run": true})))
	impact := preview["result"].(map[string]any)
	if preview["history_id"] != nil || impact["unavailable_tracks"] != float64(1) || impact["removed_assignments"] != float64(1) || impact["applied"] != false {
		t.Fatalf("review=%+v", preview)
	}
	before, err := colors.Read(t.Context(), library)
	if err != nil || before.Revision() != assigned.Revision {
		t.Fatalf("review changed stored colors: %+v %v", before, err)
	}
	applied := runAPIJob(t, s, "track.color.reconcile", string(mustJSON(t, map[string]any{"revision": assigned.Revision, "plan_token": impact["plan_token"]})))
	appliedResult := applied["result"].(map[string]any)
	if applied["history_id"] == nil || appliedResult["applied"] != true || appliedResult["plan_token"] != impact["plan_token"] || appliedResult["removed_assignments"] != float64(1) || appliedResult["revision"] == assigned.Revision {
		t.Fatalf("apply=%+v", applied)
	}
	if _, err := history.Undo(t.Context(), library, applied["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := colors.Read(t.Context(), library)
	if err != nil || restored.Revision() != before.Revision() {
		t.Fatalf("undo=%+v %v", restored, err)
	}
}

func TestTrackColorReconciliationBindsMissingIdentitiesBeforeSafety(t *testing.T) {
	s, library := testAPI(t)
	s.config.NoBackup = false
	engine, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	initial, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := colors.Assign(t.Context(), engine, library, colors.Edit{Track: "1", Color: "#123456", Revision: initial.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	state, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	state.Assignments = append(state.Assignments, colors.Assignment{OriginUUID: "unavailable", OriginTrackID: 99, Color: "#ABCDEF"})
	stored, err := colors.Replace(t.Context(), library, assigned.Revision, state)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := colors.Reconcile(t.Context(), library, colors.ReconcileOptions{Revision: stored.Revision(), DryRun: true})
	if err != nil || preview.UnavailableTracks != 1 {
		t.Fatalf("review=%+v %v", preview, err)
	}
	// Replace the missing identity while preserving both count and companion revision.
	if _, err := engine.Exec("UPDATE Track SET originDatabaseUuid='unavailable', originTrackId=99 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	result, id, err := s.RunOperation(t.Context(), "track.color.reconcile", mustJSON(t, map[string]any{"revision": stored.Revision(), "plan_token": preview.PlanToken}))
	if progress.ErrorCode(err) != "color_plan_stale" || result != nil || id != "" {
		t.Fatalf("stale review result=%v history=%q error=%v", result, id, err)
	}
	current, err := colors.Read(t.Context(), library)
	if err != nil || current.Revision() != stored.Revision() {
		t.Fatalf("stale review changed colors: %+v %v", current, err)
	}
	for _, name := range []string{"backups", "history"} {
		if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
			t.Fatalf("stale review enrolled %s: %v", name, err)
		}
	}
}
