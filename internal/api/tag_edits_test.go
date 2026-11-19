package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

func TestTagEditReviewsAsAReadAndWritesTheReviewOnce(t *testing.T) {
	s, lib := testAPI(t)
	ctx := context.Background()
	path := filepath.Join(lib, "song.mp3")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		switch tool {
		case "ffmpeg":
			return []byte("SHA256=same"), nil
		case "ffprobe":
			return []byte(`{"format":{"tags":{"title":"After"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
		}
		if slices.Contains(args, "inspect") {
			return []byte(`{"status":"success","results":[{"status":"success","inspection":{"title":"Before","artist":"Artist"}}]}`), nil
		}
		output := filepath.Join(args[slices.Index(args, "--out-dir")+1], "tagged.mp3")
		if err := os.WriteFile(output, []byte("tagged"), 0o600); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(map[string]any{"status": "success", "results": []map[string]any{{"status": "success", "output_file": output}}})
	}
	guarded := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		guarded++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
	edits := `"edits":[{"identifier":"1","title":"After","artist":"Artist"}]`
	review, id, err := s.RunOperation(ctx, "track.tags.edit", json.RawMessage(`{`+edits+`,"dry_run":true}`))
	if err != nil || id != "" || guarded != 0 {
		t.Fatalf("review %v history %q guard calls %d", err, id, guarded)
	}
	plan := review.(*track.TagEditPlan)
	if plan.ChangedFields != 1 || plan.Files[0].Changes[0].After != "After" {
		t.Fatalf("plan %+v", plan)
	}
	_, id, err = s.RunOperation(ctx, "track.tags.edit", json.RawMessage(`{`+edits+`,"plan_token":"`+plan.PlanToken+`"}`))
	if err != nil || id == "" || guarded != 1 {
		t.Fatalf("write %v history %q guard calls %d", err, id, guarded)
	}
	title := func() string {
		value, err := s.database(ctx, func(d *sql.DB) (any, error) {
			var title string
			return title, d.QueryRow(`SELECT title FROM Track WHERE id=1`).Scan(&title)
		})
		if err != nil {
			t.Fatal(err)
		}
		return value.(string)
	}
	if data, _ := os.ReadFile(path); string(data) != "tagged" || title() != "After" {
		t.Fatalf("file %q title %q", data, title())
	}
	if _, err := history.Undo(ctx, lib, id, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "original" || title() != "Curated title" {
		t.Fatalf("undo file %q title %q", data, title())
	}
	if _, _, err := s.RunOperation(ctx, "track.tags.edit", json.RawMessage(`{"edits":[]}`)); err == nil {
		t.Fatal("accepted an empty edit")
	}
}
