package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

// coverRunner answers ffprobe with an attached cover and ffmpeg with square JPEGs of the size asked.
func coverRunner(t *testing.T) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1}}]}`), nil
		}
		size := 16
		for _, arg := range args {
			if strings.Contains(arg, "scale=1400:1400") {
				size = 1400
			}
			if strings.Contains(arg, "scale=256:256") {
				size = 256
			}
		}
		var output bytes.Buffer
		if err := jpeg.Encode(&output, image.NewRGBA(image.Rect(0, 0, size, size)), nil); err != nil {
			t.Fatal(err)
		}
		return output.Bytes(), nil
	}
}

func TestArtworkImportReviewsAsAReadAndAppliesOneUndoableChange(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.config.ToolRunner = coverRunner(t)
	guarded := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		guarded++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
	review, id, err := s.RunOperation(ctx, "track.artwork.import", mustJSON(t, map[string]any{"identifiers": []string{"1"}, "dry_run": true}))
	if err != nil || id != "" || guarded != 0 {
		t.Fatalf("review %v history %q guard %d", err, id, guarded)
	}
	plan := review.(*track.ArtworkImportPlan)
	if !plan.CanApply || plan.Changed != 1 || plan.Replaced != 0 {
		t.Fatalf("plan %+v", plan)
	}
	var code *progress.Error
	if _, _, err := s.RunOperation(ctx, "track.artwork.import", mustJSON(t, map[string]any{"identifiers": []string{"1"}, "plan_token": "stale"})); !errors.As(err, &code) || code.Code != "artwork_import_plan_stale" || guarded != 0 {
		t.Fatalf("stale %v guard %d", err, guarded)
	}
	_, id, err = s.RunOperation(ctx, "track.artwork.import", mustJSON(t, map[string]any{"identifiers": []string{"1"}, "plan_token": plan.PlanToken}))
	if err != nil || id == "" || guarded != 1 {
		t.Fatalf("apply %v history %q guard %d", err, id, guarded)
	}
	cover := func() sql.NullString {
		value, err := s.database(ctx, func(d *sql.DB) (any, error) {
			var hash sql.NullString
			return hash, d.QueryRow(`SELECT albumArtSourceHash FROM Track WHERE id=1`).Scan(&hash)
		})
		if err != nil {
			t.Fatal(err)
		}
		return value.(sql.NullString)
	}
	if !cover().Valid {
		t.Fatal("library cover not set")
	}
	// One undo record restores the library's earlier cover.
	if _, err := history.Undo(ctx, lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if cover().Valid {
		t.Fatal("undo kept the imported cover")
	}
}
