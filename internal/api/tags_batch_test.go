package api

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCustomTagsBatchNoChangePreflightPreservesDesktopProtection(t *testing.T) {
	s, library := testAPI(t)
	s.config.NoBackup = false
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	initial, err := tags.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := tags.Replace(t.Context(), library, initial.Revision(), tags.State{Categories: []tags.Category{{ID: "mood", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}}}}, Assignments: []tags.Assignment{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		add    []string
		code   string
		checks int
	}{
		{"unchanged", []string{}, "tag_no_changes", 0},
		{"changed", []string{"warm"}, "engine_desktop_running", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := map[string]any{"tracks": []string{"1"}, "revision": stored.Revision, "add": test.add, "remove": []string{}, "dry_run": true}
			preview, id, err := s.RunOperation(t.Context(), "track.custom-tags.batch-update", mustJSON(t, params))
			if err != nil || id != "" {
				t.Fatalf("preview=%+v history=%q error=%v", preview, id, err)
			}
			params["dry_run"], params["plan_token"] = false, preview.(*tags.BatchChange).PlanToken
			_, id, err = s.RunOperation(t.Context(), "track.custom-tags.batch-update", mustJSON(t, params))
			if progress.ErrorCode(err) != test.code || id != "" || checks != test.checks {
				t.Fatalf("apply history=%q error=%v checks=%d", id, err, checks)
			}
			for _, name := range []string{"backups", "history"} {
				if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
					t.Fatalf("rejected apply created %s: %v", name, err)
				}
			}
		})
	}
}

func TestCustomTagsBatchReviewedAtomicUndo(t *testing.T) {
	s, library := testAPI(t)
	ctx := t.Context()
	engine, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Exec("INSERT INTO Track(id,title,path,originDatabaseUuid,originTrackId) VALUES(2,'Second','second.mp3','other-origin',2)"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	initial, err := tags.Read(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	seed := tags.State{Categories: []tags.Category{{ID: "mood", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}}}}, Assignments: []tags.Assignment{}}
	stored, err := tags.Replace(ctx, library, initial.Revision(), seed)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"tracks": []string{"2", "1", "2"}, "revision": stored.Revision, "add": []string{"warm"}, "remove": []string{}, "dry_run": true}
	preview, historyID, err := s.RunOperation(ctx, "track.custom-tags.batch-update", mustJSON(t, params))
	if err != nil || historyID != "" {
		t.Fatalf("preview=%+v history=%q error=%v", preview, historyID, err)
	}
	impact := preview.(*tags.BatchChange)
	if impact.ChangedTracks != 2 || impact.AddedAssignments != 2 || !slices.Equal(impact.TrackIDs, []int64{2, 1}) || impact.Applied {
		t.Fatalf("impact=%+v", impact)
	}
	params["dry_run"], params["plan_token"] = false, impact.PlanToken
	applied, historyID, err := s.RunOperation(ctx, "track.custom-tags.batch-update", mustJSON(t, params))
	if err != nil || historyID == "" || !applied.(*tags.BatchChange).Applied {
		t.Fatalf("apply=%+v history=%q error=%v", applied, historyID, err)
	}
	entries, err := history.List(library)
	if err != nil || len(entries) != 1 {
		t.Fatalf("one atomic history entry: %+v %v", entries, err)
	}
	params["revision"], params["dry_run"] = applied.(*tags.BatchChange).Revision, true
	noChange, _, err := s.RunOperation(ctx, "track.custom-tags.batch-update", mustJSON(t, params))
	if err != nil || noChange.(*tags.BatchChange).ChangedTracks != 0 {
		t.Fatalf("no-op preview=%+v error=%v", noChange, err)
	}
	params["dry_run"], params["plan_token"] = false, noChange.(*tags.BatchChange).PlanToken
	unchanged, noHistory, err := s.RunOperation(ctx, "track.custom-tags.batch-update", mustJSON(t, params))
	if progress.ErrorCode(err) != "tag_no_changes" || noHistory != "" || unchanged != nil {
		t.Fatalf("no-op apply=%+v history=%q error=%v", unchanged, noHistory, err)
	}
	entries, err = history.List(library)
	if err != nil || len(entries) != 1 {
		t.Fatalf("no-op created a history entry: %+v %v", entries, err)
	}
	if _, err := history.Undo(ctx, library, historyID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := tags.Read(ctx, library)
	if err != nil || len(restored.Assignments) != 0 {
		t.Fatalf("undo=%+v error=%v", restored, err)
	}
}

func TestCustomTagsBatchPreflightHasNoHistorySideEffects(t *testing.T) {
	for _, test := range []struct {
		name        string
		add, remove []string
		code        string
	}{
		{"overlap", []string{"warm"}, []string{"warm"}, "tag_actions_overlap"},
		{"duplicate", []string{"warm", "warm"}, []string{}, "tag_value_duplicate"},
		{"unknown", []string{"missing"}, []string{}, "tag_not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, library := testAPI(t)
			initial, err := tags.Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := tags.Replace(t.Context(), library, initial.Revision(), tags.State{Categories: []tags.Category{{ID: "mood", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}}}}, Assignments: []tags.Assignment{}})
			if err != nil {
				t.Fatal(err)
			}
			_, id, err := s.RunOperation(t.Context(), "track.custom-tags.batch-update", mustJSON(t, map[string]any{"tracks": []string{"1"}, "revision": stored.Revision, "add": test.add, "remove": test.remove, "dry_run": true}))
			if progress.ErrorCode(err) != test.code || id != "" {
				t.Fatalf("history=%q error=%v want=%s", id, err, test.code)
			}
			entries, err := history.List(library)
			if err != nil || len(entries) != 0 {
				t.Fatalf("preflight left history: %+v %v", entries, err)
			}
		})
	}
}
