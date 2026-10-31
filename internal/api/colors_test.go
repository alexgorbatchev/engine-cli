package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestTrackColorAPIReadWriteClearAndUndo(t *testing.T) {
	s, library := testAPI(t)
	initial := runAPIJob(t, s, "track.color.list", `{}`)["result"].(map[string]any)
	if len(initial["tracks"].([]any)) != 0 {
		t.Fatalf("unexpected initial colors: %+v", initial)
	}
	if _, err := os.Stat(companion.ColorsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("read enrolled colors: %v", err)
	}
	assigned := runAPIJob(t, s, "track.color.update", string(mustJSON(t, map[string]any{"track": "1", "revision": initial["revision"], "color": "#aB1234"})))
	result := assigned["result"].(map[string]any)
	if result["color"] != "#AB1234" || result["track_id"] != float64(1) || result["changed"] != true || assigned["history_id"] == nil {
		t.Fatalf("assignment=%+v", assigned)
	}
	inspected := runAPIJob(t, s, "track.color.inspect", `{"track":"1"}`)["result"].(map[string]any)
	if inspected["revision"] != result["revision"] || inspected["color"] != result["color"] {
		t.Fatalf("inspect=%+v assignment=%+v", inspected, result)
	}
	listed := runAPIJob(t, s, "track.color.list", `{}`)["result"].(map[string]any)
	rows := listed["tracks"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["color"] != result["color"] || listed["revision"] != result["revision"] {
		t.Fatalf("collection projection=%+v", listed)
	}
	cleared := runAPIJob(t, s, "track.color.update", string(mustJSON(t, map[string]any{"track": "1", "revision": result["revision"], "color": ""})))
	if clearResult := cleared["result"].(map[string]any); clearResult["color"] != "" || clearResult["changed"] != true {
		t.Fatalf("clear=%+v", cleared)
	}
	if _, err := history.Undo(t.Context(), library, cleared["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored := runAPIJob(t, s, "track.color.inspect", `{"track":"1"}`)["result"].(map[string]any)
	if restored["color"] != result["color"] || restored["revision"] != result["revision"] {
		t.Fatalf("undo=%+v", restored)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("color write enrolled custom tags: %v", err)
	}
}

func TestRejectedTrackColorsDoNotEnrollSafetyOrStorage(t *testing.T) {
	for _, test := range []struct {
		name, raw, code string
	}{
		{"missing color", `{"track":"1","revision":"REVISION"}`, "operation_failed"},
		{"null color", `{"track":"1","revision":"REVISION","color":null}`, "operation_failed"},
		{"invalid color", `{"track":"1","revision":"REVISION","color":"red"}`, "color_invalid"},
		{"whitespace is not clear", `{"track":"1","revision":"REVISION","color":" "}`, "color_invalid"},
		{"unknown track", `{"track":"999","revision":"REVISION","color":"#123456"}`, "track_not_found"},
		{"stale clear", `{"track":"1","revision":"stale","color":""}`, "color_revision_stale"},
		{"already unassigned", `{"track":"1","revision":"REVISION","color":""}`, "color_no_changes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, library := testAPI(t)
			s.config.NoBackup = false
			state, err := colors.Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			params := []byte(test.raw)
			// Parameters are encoded through the same native binder as HTTP jobs.
			params = []byte(strings.ReplaceAll(string(params), "REVISION", state.Revision()))
			_, id, err := s.RunOperation(t.Context(), "track.color.update", params)
			if progress.ErrorCode(err) != test.code || id != "" {
				t.Fatalf("rejection: code=%s history=%q error=%v", progress.ErrorCode(err), id, err)
			}
			for _, path := range []string{companion.ColorsPath(library), filepath.Join(library, ".engine-cli", "backups"), filepath.Join(library, ".engine-cli", "history")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("rejection enrolled %s: %v", path, err)
				}
			}
		})
	}
}

func TestTrackRemovalPrunesOnlyItsColorsAndUndoRestores(t *testing.T) {
	s, library := testAPI(t)
	initial := runAPIJob(t, s, "track.color.inspect", `{"track":"1"}`)["result"].(map[string]any)
	runAPIJob(t, s, "track.color.update", string(mustJSON(t, map[string]any{"track": "1", "revision": initial["revision"], "color": "#123456"})))
	state, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	expected := state.Revision()
	state.Assignments = append(state.Assignments, colors.Assignment{OriginUUID: "earlier-unavailable", OriginTrackID: 99, Color: "#ABCDEF"})
	before, err := colors.Replace(t.Context(), library, expected, state)
	if err != nil {
		t.Fatal(err)
	}
	removed := runAPIJob(t, s, "track.rm", `{"tracks":["1"]}`)
	after, err := colors.Read(t.Context(), library)
	if err != nil || len(after.Assignments) != 1 || after.Assignments[0].OriginUUID != "earlier-unavailable" {
		t.Fatalf("removed colors=%+v error=%v", after, err)
	}
	if _, err := history.Undo(t.Context(), library, removed["history_id"].(string), history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := colors.Read(t.Context(), library)
	if err != nil || restored.Revision() != before.Revision() {
		t.Fatalf("undo colors=%+v error=%v", restored, err)
	}
}
