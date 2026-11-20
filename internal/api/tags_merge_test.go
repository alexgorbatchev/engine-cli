package api

import (
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCustomTagMergeReviewsAppliesAndUndoes(t *testing.T) {
	s, lib := testAPI(t)
	ctx := t.Context()
	state, err := tags.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	category, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Vibe", Revision: state.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	hypnotic, err := tags.EditValue(ctx, lib, tags.Edit{Category: category.ID, Name: "Hypnotic", Revision: category.Revision})
	if err != nil {
		t.Fatal(err)
	}
	typo, err := tags.EditValue(ctx, lib, tags.Edit{Category: category.ID, Name: "Hypnotc", Revision: hypnotic.Revision})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	assigned, err := tags.Assign(ctx, engine, lib, tags.AssignmentEdit{Track: "1", Revision: typo.Revision, Values: []string{typo.ID}})
	if err != nil {
		t.Fatal(err)
	}
	review := map[string]any{"id": typo.ID, "into": hypnotic.ID, "revision": assigned.Revision, "dry_run": true}
	preview := runAPIJob(t, s, "tag.value.merge", string(mustJSON(t, review)))
	impact := preview["result"].(map[string]any)
	if preview["history_id"] != nil || impact["affected_tracks"] != float64(1) || impact["moved_assignments"] != float64(1) || impact["applied"] != false {
		t.Fatalf("merge preview=%+v", preview)
	}
	review["dry_run"] = false
	if _, id, err := s.RunOperation(ctx, "tag.value.merge", mustJSON(t, review)); progress.ErrorCode(err) != "tag_plan_stale" || id != "" {
		t.Fatalf("unreviewed merge: history=%q error=%v", id, err)
	}
	review["plan_token"] = impact["plan_token"]
	applied := runAPIJob(t, s, "tag.value.merge", string(mustJSON(t, review)))
	if applied["history_id"] == nil || applied["result"].(map[string]any)["applied"] != true {
		t.Fatalf("merge apply=%+v", applied)
	}
	merged, err := tags.InspectTrack(ctx, engine, lib, "1")
	if err != nil || len(merged.Values) != 1 || merged.Values[0] != hypnotic.ID {
		t.Fatalf("merged track=%+v %v", merged, err)
	}
	if _, err := history.Undo(ctx, lib, applied["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := tags.Read(ctx, lib)
	if err != nil || restored.Revision() != assigned.Revision {
		t.Fatalf("undo=%+v %v", restored, err)
	}
}
