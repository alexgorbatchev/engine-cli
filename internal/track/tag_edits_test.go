package track

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// tagEditFixture serves tag-track inspections from a table of each file's
// tags and records the arguments of every update.
type tagEditFixture struct {
	t       *testing.T
	d       *sql.DB
	lib     string
	tags    map[string]acquire.Inspection
	updates [][]string
	// fail names a file whose update fails; tool fails every inspection.
	fail, tool string
}

func (f *tagEditFixture) run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "tag-track":
		path := args[len(args)-1]
		if f.tool != "" {
			return nil, &progress.Error{Code: f.tool, Cause: errors.New("fixture: tag-track missing")}
		}
		if slices.Contains(args, "inspect") {
			inspection := f.tags[filepath.Base(path)]
			return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", Inspection: &inspection}}})
		}
		if filepath.Base(path) == f.fail {
			return nil, errors.New("fixture: write refused")
		}
		f.updates = append(f.updates, args)
		output := filepath.Join(args[slices.Index(args, "--out-dir")+1], "tagged.mp3")
		if err := os.WriteFile(output, []byte("tagged"), 0o600); err != nil {
			f.t.Fatal(err)
		}
		return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: output}}})
	case "ffmpeg":
		return []byte("SHA256=same"), nil
	case "ffprobe":
		return []byte(`{"format":{"tags":{"title":"New"}},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`), nil
	}
	f.t.Fatalf("unexpected %s", name)
	return nil, nil
}

func (f *tagEditFixture) options() TagEditOptions {
	return TagEditOptions{Library: f.lib, Runner: f.run}
}

func newTagEditFixture(t *testing.T) *tagEditFixture {
	t.Helper()
	d, lib, _ := createTestDB(t)
	f := &tagEditFixture{t: t, d: d, lib: lib, tags: map[string]acquire.Inspection{}}
	for i, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
		f.tags[name] = acquire.Inspection{Title: "Old " + name, Artist: "Artist", Album: "Album", Genre: "House", Date: "2020", Comment: "promo"}
		if _, err := d.Exec(`INSERT INTO Track(id,title,path) VALUES(?,?,?)`, i+1, "Track "+name, name); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func text(value string) *string { return &value }

// titleEdits changes each Title, repeats b's Artist as it already is, and clears c's Comment.
func titleEdits() []TagEdit {
	return []TagEdit{
		{Identifier: "1", Title: text("New a")},
		{Identifier: "2", Title: text("New b"), Artist: text("Artist")},
		{Identifier: "3", Title: text("New c"), Comment: text("")},
	}
}

func TestTagEditsReviewOnlyChangedFieldsFromTheFiles(t *testing.T) {
	f := newTagEditFixture(t)
	plan, err := PreviewTagEdits(context.Background(), f.d, titleEdits(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	if plan.ChangedFiles != 3 || plan.ChangedFields != 4 || plan.ClearedFields != 1 || !plan.CanApply || plan.PlanToken == "" {
		t.Fatalf("plan %+v", plan)
	}
	if got := plan.Files[1]; got.TrackID != 2 || got.Path != "b.mp3" || len(got.Changes) != 1 || got.Changes[0] != (TagChange{Field: "title", Before: "Old b.mp3", After: "New b"}) {
		t.Fatalf("file b %+v", got)
	}
	if got := plan.Files[2].Changes; len(got) != 2 || got[1] != (TagChange{Field: "comment", Before: "promo", After: ""}) {
		t.Fatalf("file c changes %+v", got)
	}
	if len(f.updates) != 0 {
		t.Fatalf("the review wrote %v", f.updates)
	}
	again, err := PreviewTagEdits(context.Background(), f.d, titleEdits(), f.options())
	if err != nil || again.PlanToken != plan.PlanToken {
		t.Fatalf("an unchanged review produced another token: %v", err)
	}
	// A file read again after it changed binds a different token.
	if err := os.WriteFile(filepath.Join(f.lib, "a.mp3"), []byte("audio, edited elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, _ := PreviewTagEdits(context.Background(), f.d, titleEdits(), f.options()); changed.PlanToken == plan.PlanToken {
		t.Fatal("a changed file kept the token")
	}
}

func TestTagEditsWriteExactlyTheReviewedFieldsInPlace(t *testing.T) {
	f := newTagEditFixture(t)
	ctx, edits := context.Background(), titleEdits()
	plan, err := PreviewTagEdits(ctx, f.d, edits, f.options())
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyTagEdits(ctx, f.d, edits, plan.PlanToken, f.options())
	if err != nil || result.Status != "success" || result.Succeeded != 3 || !slices.Equal(result.TrackIDs, []int64{1, 2, 3}) {
		t.Fatalf("result %+v %v", result, err)
	}
	for i, args := range f.updates {
		joined := strings.Join(args, " ")
		// Only the reviewed fields reach tag-track, and never with a lookup.
		if !strings.Contains(joined, "--title New") || !strings.Contains(joined, "--no-lookup") || slices.ContainsFunc([]string{"--artist", "--album", "--genre", "--date"}, func(flag string) bool { return strings.Contains(joined, flag) }) {
			t.Fatalf("update %d args %v", i, args)
		}
		if (i == 2) != strings.Contains(joined, "--comment") {
			t.Fatalf("update %d comment args %v", i, args)
		}
	}
	for _, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		if data, err := os.ReadFile(filepath.Join(f.lib, name)); err != nil || string(data) != "tagged" {
			t.Fatalf("%s not written in place: %q %v", name, data, err)
		}
	}
}

func TestTagEditsRefuseStaleAndUnchangedReviews(t *testing.T) {
	f := newTagEditFixture(t)
	ctx, edits := context.Background(), titleEdits()
	var code *progress.Error
	if err := CheckTagEdits(ctx, f.d, edits, "stale", f.options()); !errors.As(err, &code) || code.Code != "file_tags_plan_stale" {
		t.Fatalf("stale token %v", err)
	}
	if err := CheckTagEdits(ctx, f.d, edits, "", f.options()); err == nil {
		t.Fatal("applied without a review")
	}
	same := []TagEdit{{Identifier: "1", Title: text("Old a.mp3")}}
	plan, err := PreviewTagEdits(ctx, f.d, same, f.options())
	if err != nil || plan.CanApply {
		t.Fatalf("unchanged plan %+v %v", plan, err)
	}
	if _, err := ApplyTagEdits(ctx, f.d, same, plan.PlanToken, f.options()); !errors.As(err, &code) || code.Code != "file_tags_unchanged" {
		t.Fatalf("unchanged apply %v", err)
	}
	if len(f.updates) != 0 {
		t.Fatalf("a refused review wrote %v", f.updates)
	}
}

func TestTagEditsReportEachFileThatFails(t *testing.T) {
	f := newTagEditFixture(t)
	ctx := context.Background()
	if err := os.Remove(filepath.Join(f.lib, "c.mp3")); err != nil {
		t.Fatal(err)
	}
	edits := append(titleEdits(), TagEdit{Identifier: "99", Title: text("Ghost")})
	plan, err := PreviewTagEdits(ctx, f.d, edits, f.options())
	if err != nil {
		t.Fatal(err)
	}
	if plan.ChangedFiles != 2 || plan.Files[2].ErrorCode != "file_not_found" || plan.Files[3].ErrorCode != "not_found" {
		t.Fatalf("plan %+v", plan.Files)
	}
	f.fail = "b.mp3"
	result, err := ApplyTagEdits(ctx, f.d, edits, plan.PlanToken, f.options())
	if err != nil || result.Status != "partial" || result.Succeeded != 1 || result.Failed != 1 || result.Files[1].Error == "" {
		t.Fatalf("partial result %+v %v", result, err)
	}
	// Every changed file failing fails the job, keeping the per-file results.
	f.fail = "a.mp3"
	only := []TagEdit{{Identifier: "1", Title: text("Again")}}
	plan, err = PreviewTagEdits(ctx, f.d, only, f.options())
	if err != nil {
		t.Fatal(err)
	}
	var code *progress.Error
	if result, err := ApplyTagEdits(ctx, f.d, only, plan.PlanToken, f.options()); !errors.As(err, &code) || code.Code != "batch_failed" || result.Status != "failed" {
		t.Fatalf("all failed %+v %v", result, err)
	}
}

func TestTagEditsValidateBeforeReadingFiles(t *testing.T) {
	f := newTagEditFixture(t)
	f.tool = "tool_unavailable"
	ctx := context.Background()
	var code *progress.Error
	for _, edits := range [][]TagEdit{
		nil,
		{{Identifier: " "}},
		{{Identifier: "1"}, {Identifier: "1"}},
		{{Identifier: "1", Date: text("next friday")}},
		make([]TagEdit, MaxTagEdits+1),
	} {
		if _, err := PreviewTagEdits(ctx, f.d, edits, f.options()); err == nil {
			t.Fatalf("accepted %+v", edits)
		}
	}
	if _, err := PreviewTagEdits(ctx, f.d, []TagEdit{{Identifier: "1", Date: text("next friday")}}, f.options()); !errors.As(err, &code) || code.Code != "invalid_tag_date" {
		t.Fatalf("date code %v", err)
	}
	for _, date := range []string{"", "2024", "2024-05", "2024-05-17"} {
		if err := ValidateTagEdits([]TagEdit{{Identifier: "1", Date: text(date)}}); err != nil {
			t.Fatalf("refused date %q: %v", date, err)
		}
	}
	// A missing tool fails the whole review, so the dock can offer the Engine update.
	if _, err := PreviewTagEdits(ctx, f.d, titleEdits(), f.options()); !errors.As(err, &code) || code.Code != "tool_unavailable" {
		t.Fatalf("missing tool %v", err)
	}
}
