package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

func TestTagCopiesRepairReviewsFoldsAndUndoes(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	copyPath, original := filepath.Join(lib, "song-upgraded-42.mp3"), filepath.Join(lib, "song.mp3")
	for path, content := range map[string]string{original: "old tags", copyPath: "new tags"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.database(ctx, func(d *sql.DB) (any, error) {
		return d.Exec(`UPDATE Track SET path='song-upgraded-42.mp3' WHERE id=1`)
	}); err != nil {
		t.Fatal(err)
	}
	s.config.ToolRunner = func(context.Context, string, ...string) ([]byte, error) { return []byte("SHA256=same"), nil }
	guarded := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		guarded++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
	review, _, err := s.RunOperation(ctx, "library.tag-copies.repair", json.RawMessage(`{"dry_run":true}`))
	if err != nil || guarded != 0 {
		t.Fatalf("review %v guard calls %d", err, guarded)
	}
	plan := review.(*track.TagCopyPlan)
	if len(plan.Folds) != 1 || plan.Folds[0].To != "song.mp3" || !plan.Folds[0].ReplacesOriginal {
		t.Fatalf("plan %+v", plan)
	}
	var code *progress.Error
	if _, _, err := s.RunOperation(ctx, "library.tag-copies.repair", json.RawMessage(`{"plan_token":"other"}`)); !errors.As(err, &code) || code.Code != "tag_copies_stale" || guarded != 0 {
		t.Fatalf("stale apply %v guard calls %d", err, guarded)
	}
	_, id, err := s.RunOperation(ctx, "library.tag-copies.repair", mustJSON(t, map[string]any{"plan_token": plan.PlanToken}))
	if err != nil || id == "" || guarded != 1 {
		t.Fatalf("apply %s %v guard calls %d", id, err, guarded)
	}
	stored := func() string {
		value, err := s.database(ctx, func(d *sql.DB) (any, error) {
			var path string
			return path, d.QueryRow(`SELECT path FROM Track WHERE id=1`).Scan(&path)
		})
		if err != nil {
			t.Fatal(err)
		}
		return value.(string)
	}
	if data, _ := os.ReadFile(original); string(data) != "new tags" || stored() != "song.mp3" {
		t.Fatalf("folded %q stored %q", data, stored())
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("copy left behind: %v", err)
	}
	if _, err := history.Undo(ctx, lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{original: "old tags", copyPath: "new tags"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatalf("%s after undo %q %v", filepath.Base(path), data, err)
		}
	}
	if stored() != "song-upgraded-42.mp3" {
		t.Fatalf("undo stored %q", stored())
	}
}
