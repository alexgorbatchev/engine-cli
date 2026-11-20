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

func TestTrackMoveReviewsAsAReadAndAppliesTheReview(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(lib, "Sorted"), 0o755); err != nil {
		t.Fatal(err)
	}
	guarded := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		guarded++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
	body := func(extra map[string]any) json.RawMessage {
		values := map[string]any{"identifier": "1", "destination": filepath.Join(lib, "Sorted")}
		for key, value := range extra {
			values[key] = value
		}
		return mustJSON(t, values)
	}
	review, id, err := s.RunOperation(ctx, "track.move", body(map[string]any{"dry_run": true}))
	if err != nil || id != "" || guarded != 0 {
		t.Fatalf("review %v history %q guard %d", err, id, guarded)
	}
	plan := review.(*track.MovePlan)
	if !plan.CanApply || plan.To != "Sorted/song.mp3" || len(plan.SharedWith) != 0 {
		t.Fatalf("plan %+v", plan)
	}
	var code *progress.Error
	if _, _, err := s.RunOperation(ctx, "track.move", body(map[string]any{"plan_token": "stale"})); !errors.As(err, &code) || code.Code != "move_plan_stale" || guarded != 0 {
		t.Fatalf("stale %v guard %d", err, guarded)
	}
	_, id, err = s.RunOperation(ctx, "track.move", body(map[string]any{"plan_token": plan.PlanToken}))
	if err != nil || id == "" || guarded != 1 {
		t.Fatalf("apply %v history %q guard %d", err, id, guarded)
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
	if _, err := os.Stat(filepath.Join(lib, "Sorted", "song.mp3")); err != nil || stored() != "Sorted/song.mp3" {
		t.Fatalf("moved %v stored %q", err, stored())
	}
	// One undo record puts the file and the stored path back.
	if _, err := history.Undo(ctx, lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "song.mp3")); err != nil || stored() != "song.mp3" {
		t.Fatalf("undo %v stored %q", err, stored())
	}
}

func TestLibraryFoldersListsOnlyMoveRoots(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.Mkdir(filepath.Join(lib, "Sorted"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, id, err := s.RunOperation(context.Background(), "library.folders", json.RawMessage(`{}`))
	listing, ok := result.(*track.FolderListing)
	if err != nil || id != "" || !ok || listing.Roots[0].Label != "Library" || len(listing.Folders) == 0 {
		t.Fatalf("listing %+v %v", result, err)
	}
	var code *progress.Error
	if _, _, err := s.RunOperation(context.Background(), "library.folders", mustJSON(t, map[string]any{"path": t.TempDir()})); !errors.As(err, &code) || code.Code != "folder_outside_roots" {
		t.Fatalf("outside %v", err)
	}
}
