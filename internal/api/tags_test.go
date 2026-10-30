package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCustomTagRejectedEditsDoNotCreateRecoveryFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation string
		params    map[string]any
		code      string
	}{
		{"duplicate category", "tag.category.create", map[string]any{"name": "  MOOD  "}, "tag_name_exists"},
		{"duplicate category rename", "tag.category.update", map[string]any{"id": "energy", "name": "Mood"}, "tag_name_exists"},
		{"duplicate value", "tag.value.create", map[string]any{"category": "mood", "name": "  WARM  "}, "tag_name_exists"},
		{"duplicate value rename", "tag.value.update", map[string]any{"id": "cool", "name": "Warm"}, "tag_name_exists"},
		{"invalid name", "tag.category.create", map[string]any{"name": "bad\nname"}, "tag_name_invalid"},
		{"missing category", "tag.category.update", map[string]any{"id": "missing", "name": "Other"}, "tag_not_found"},
		{"missing value category", "tag.value.create", map[string]any{"category": "missing", "name": "Other"}, "tag_not_found"},
		{"missing value", "tag.value.update", map[string]any{"id": "missing", "name": "Other"}, "tag_not_found"},
		{"stale edit", "tag.category.create", map[string]any{"name": "Other", "revision": "stale"}, "tag_revision_stale"},
		{"missing assignment track", "track.custom-tags.update", map[string]any{"track": "999", "values": []string{"warm"}}, "track_not_found"},
		{"missing assignment value", "track.custom-tags.update", map[string]any{"track": "1", "values": []string{"missing"}}, "tag_not_found"},
		{"stale assignment", "track.custom-tags.update", map[string]any{"track": "1", "values": []string{"warm"}, "revision": "stale"}, "tag_revision_stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, library := testAPI(t)
			s.config.NoBackup = false
			before, err := tags.Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			seed := tags.State{Categories: []tags.Category{
				{ID: "mood", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}, {ID: "cool", Name: "Cool"}}},
				{ID: "energy", Name: "Energy", Values: []tags.Value{}},
			}, Assignments: []tags.Assignment{}}
			stored, err := tags.Replace(t.Context(), library, before.Revision(), seed)
			if err != nil {
				t.Fatal(err)
			}
			if _, supplied := test.params["revision"]; !supplied {
				test.params["revision"] = stored.Revision
			}
			_, historyID, err := s.RunOperation(t.Context(), test.operation, mustJSON(t, test.params))
			if progress.ErrorCode(err) != test.code || historyID != "" {
				t.Errorf("rejected edit: history=%q error=%v code=%s; want %s", historyID, err, progress.ErrorCode(err), test.code)
			}
			for _, name := range []string{"backups", "history"} {
				if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
					t.Errorf("rejected edit created %s: %v", name, err)
				}
			}
			if entries, err := history.List(library); err != nil || len(entries) != 0 {
				t.Fatalf("rejected edit left history or pending recovery: %+v %v", entries, err)
			}
			after, err := tags.Read(t.Context(), library)
			if err != nil || after.Revision() != stored.Revision {
				t.Fatalf("rejected edit changed taxonomy: %+v %v", after, err)
			}
		})
	}
}

func TestCustomTagValidNamesAndCategoryScopedValues(t *testing.T) {
	s, library := testAPI(t)
	s.config.NoBackup = false
	state, err := tags.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.Revision()
	for _, test := range []struct {
		operation string
		params    map[string]any
	}{
		{"tag.category.create", map[string]any{"name": "Mood"}},
		{"tag.category.create", map[string]any{"name": "Energy"}},
	} {
		test.params["revision"] = revision
		result := runAPIJob(t, s, test.operation, string(mustJSON(t, test.params)))["result"].(map[string]any)
		revision = result["revision"].(string)
	}
	state, err = tags.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range state.Categories {
		created := runAPIJob(t, s, "tag.value.create", string(mustJSON(t, map[string]any{"category": category.ID, "name": "Warm", "revision": revision})))["result"].(map[string]any)
		revision = created["revision"].(string)
		renamed := runAPIJob(t, s, "tag.value.update", string(mustJSON(t, map[string]any{"id": created["id"], "name": "Bright", "revision": revision})))["result"].(map[string]any)
		revision = renamed["revision"].(string)
		updated := runAPIJob(t, s, "tag.category.update", string(mustJSON(t, map[string]any{"id": category.ID, "name": category.Name + " revised", "revision": revision})))["result"].(map[string]any)
		revision = updated["revision"].(string)
	}
	state, err = tags.Read(t.Context(), library)
	if err != nil || len(state.Categories) != 2 || state.Revision() != revision {
		t.Fatalf("valid edits: %+v %v", state, err)
	}
	for _, category := range state.Categories {
		if len(category.Values) != 1 || category.Values[0].Name != "Bright" || (category.Name != "Mood revised" && category.Name != "Energy revised") {
			t.Fatalf("scoped values and renamed category: %+v", category)
		}
	}
}

func TestCustomTagCatalogClearableRequiredValues(t *testing.T) {
	s, _ := testAPI(t)
	op := s.operations["track.custom-tags.update"]
	if op.Parameters["values"] != "[]string" || !slices.Contains(op.Required, "values") {
		t.Fatalf("catalog omits required replacement array: %+v", op)
	}
	for _, raw := range []string{`{"track":"1","revision":"current"}`, `{"track":"1","revision":"current","values":null}`, `{"track":"1","revision":"current","values":["duplicate","duplicate"]}`, `{"track":"1","revision":"current","values":[1]}`} {
		if _, err := op.bind(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid replacement: %s", raw)
		}
	}
	if _, err := op.bind(json.RawMessage(`{"track":"1","revision":"current","values":[]}`)); err != nil {
		t.Fatalf("clear rejected: %v", err)
	}
	ordinary := s.operations["playlist.track.add"]
	if _, err := ordinary.bind(json.RawMessage(`{"playlist":"1","tracks":[]}`)); err == nil {
		t.Fatal("ordinary nonempty required list weakened")
	}
}

func TestCustomTagAPIReviewAssignmentAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	listed := runAPIJob(t, s, "tag.category.list", `{}`)["result"].(map[string]any)
	if _, err := os.Stat(companion.TagsPath(lib)); !os.IsNotExist(err) {
		t.Fatalf("read enrolled companion: %v", err)
	}
	category := runAPIJob(t, s, "tag.category.create", string(mustJSON(t, map[string]any{"name": "Mood", "revision": listed["revision"]})))["result"].(map[string]any)
	value := runAPIJob(t, s, "tag.value.create", string(mustJSON(t, map[string]any{"category": category["id"], "name": "Warm", "revision": category["revision"]})))["result"].(map[string]any)
	assigned := runAPIJob(t, s, "track.custom-tags.update", string(mustJSON(t, map[string]any{"track": "1", "revision": value["revision"], "values": []string{value["id"].(string)}})))
	result := assigned["result"].(map[string]any)
	if len(result["values"].([]any)) != 1 || assigned["history_id"] == nil {
		t.Fatalf("assignment job: %+v", assigned)
	}
	preview := runAPIJob(t, s, "tag.category.delete", string(mustJSON(t, map[string]any{"id": category["id"], "revision": result["revision"], "dry_run": true})))
	impact := preview["result"].(map[string]any)
	if preview["history_id"] != nil || impact["affected_tracks"] != float64(1) || impact["removed_assignments"] != float64(1) {
		t.Fatalf("delete preview: %+v", preview)
	}
	cleared := runAPIJob(t, s, "track.custom-tags.update", string(mustJSON(t, map[string]any{"track": "1", "revision": result["revision"], "values": []string{}})))
	if got := cleared["result"].(map[string]any)["values"].([]any); len(got) != 0 {
		t.Fatalf("clear retained values: %v", got)
	}
	if _, err := history.Undo(ctx, lib, cleared["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	state, err := tags.List(ctx, lib)
	if err != nil || state.Categories[0].Values[0].TrackCount != 1 {
		t.Fatalf("assignment undo: %+v %v", state, err)
	}
	entries, err := os.ReadDir(filepath.Join(lib, ".engine-cli", "history"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("durable history missing: %v", err)
	}
}

func TestCustomTagReconciliationJobPreviewApplyAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	ctx := t.Context()
	state, err := tags.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	category, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Mood", Revision: state.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	value, err := tags.EditValue(ctx, lib, tags.Edit{Category: category.ID, Name: "Warm", Revision: category.Revision})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	assigned, err := tags.Assign(ctx, engine, lib, tags.AssignmentEdit{Track: "1", Revision: value.Revision, Values: []string{value.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Exec("DELETE FROM Track WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	preview := runAPIJob(t, s, "tag.reconcile", string(mustJSON(t, map[string]any{"revision": assigned.Revision, "dry_run": true})))
	impact := preview["result"].(map[string]any)
	if preview["history_id"] != nil || impact["unavailable_tracks"] != float64(1) || impact["removed_assignments"] != float64(1) || impact["applied"] != false {
		t.Fatalf("reconcile preview=%+v", preview)
	}
	before, err := tags.Read(ctx, lib)
	if err != nil || before.Revision() != assigned.Revision {
		t.Fatalf("preview mutated assignments: %+v %v", before, err)
	}
	applied := runAPIJob(t, s, "tag.reconcile", string(mustJSON(t, map[string]any{"revision": assigned.Revision, "dry_run": false, "plan_token": impact["plan_token"]})))
	result := applied["result"].(map[string]any)
	if applied["history_id"] == nil || result["applied"] != true || result["plan_token"] != impact["plan_token"] || result["removed_assignments"] != float64(1) || result["revision"] == assigned.Revision {
		t.Fatalf("reconcile apply=%+v", applied)
	}
	if _, err := history.Undo(ctx, lib, applied["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := tags.Read(ctx, lib)
	if err != nil || restored.Revision() != before.Revision() {
		t.Fatalf("reconcile undo=%+v %v", restored, err)
	}
}
