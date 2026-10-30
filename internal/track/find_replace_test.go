package track

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestFindReplacePreflightRequiresExactReview(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(1,'before','one.mp3')"); err != nil {
		t.Fatal(err)
	}
	opts := FindReplaceOptions{Identifiers: []string{"1"}, Fields: []string{"title"}, Find: "before", Replacement: new("after")}
	plan, err := PreviewFindReplace(t.Context(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFindReplace(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("unreviewed preflight accepted: %v", err)
	}
	opts.PlanToken = plan.PlanToken
	if checked, err := CheckFindReplace(t.Context(), d, opts); err != nil || checked.PlanToken != plan.PlanToken {
		t.Fatalf("exact preflight failed: %+v %v", checked, err)
	}
	opts.Fields = []string{"path"}
	if _, err := CheckFindReplace(t.Context(), d, opts); err == nil {
		t.Fatal("invalid preflight accepted")
	}
}

func TestFindReplaceRejectsAmplification(t *testing.T) {
	for _, tc := range []struct{ name, find, replacement, mode string }{
		{"literal", "x", strings.Repeat("y", 1024), "literal"},
		{"single capture", "^(.*)$", strings.Repeat("${1}", 1024), "regex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := createTestDB(t)
			before := strings.Repeat("x", 2048)
			if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(1,?,'one.mp3')", before); err != nil {
				t.Fatal(err)
			}
			opts := FindReplaceOptions{Identifiers: []string{"1"}, Fields: []string{"title"}, Find: tc.find, Replacement: &tc.replacement, Mode: tc.mode}
			if _, err := PreviewFindReplace(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_too_large" {
				t.Fatalf("amplification accepted: %v", err)
			}
			var after string
			if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&after); err != nil || after != before {
				t.Fatalf("rejection changed metadata: %v", err)
			}
		})
	}
}

func TestFindReplaceSourceAndAggregateBudgets(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count, size int
		field       string
	}{
		{"selected oversized field", 1, (1 << 20) + 1, "title"},
		{"unselected evidence field", 1, (1 << 20) + 1, "comment"},
		{"aggregate evidence", 17, 1 << 20, "comment"},
		{"aggregate output", 17, 65536, "title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := createTestDB(t)
			ids := []string{}
			for id := 1; id <= tc.count; id++ {
				if _, err := d.Exec("INSERT INTO Track(id,title,comment,path) VALUES(?, 'x', '', ?)", id, fmt.Sprintf("%d.mp3", id)); err != nil {
					t.Fatal(err)
				}
				if _, err := d.Exec("UPDATE Track SET "+tc.field+"=? WHERE id=?", strings.Repeat("x", tc.size), id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, fmt.Sprint(id))
			}
			opts := FindReplaceOptions{Identifiers: ids, Fields: []string{"title"}, Find: "x", Replacement: new("y")}
			if tc.name == "aggregate output" {
				opts.Find, opts.Mode, opts.Replacement = "^(.*)$", "regex", new(strings.Repeat("${1}", 16))
			}
			if _, err := PreviewFindReplace(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_too_large" {
				t.Fatalf("accepted oversized plan: %v", err)
			}
		})
	}
}

func TestFindReplaceValidation(t *testing.T) {
	valid := FindReplaceOptions{Identifiers: []string{"1"}, Fields: []string{"title"}, Find: "old", Replacement: new("new")}
	for _, tc := range []struct {
		name   string
		change func(*FindReplaceOptions)
	}{
		{"empty selection", func(o *FindReplaceOptions) { o.Identifiers = nil }},
		{"large selection", func(o *FindReplaceOptions) { o.Identifiers = make([]string, MaxMetadataBatch+1) }},
		{"blank selector", func(o *FindReplaceOptions) { o.Identifiers = []string{" "} }},
		{"no fields", func(o *FindReplaceOptions) { o.Fields = nil }},
		{"unsafe field", func(o *FindReplaceOptions) { o.Fields = []string{"path"} }},
		{"duplicate field", func(o *FindReplaceOptions) { o.Fields = []string{"title", "title"} }},
		{"absent replacement", func(o *FindReplaceOptions) { o.Replacement = nil }},
		{"invalid replacement utf8", func(o *FindReplaceOptions) { o.Replacement = new(string([]byte{0xff})) }},
		{"large replacement", func(o *FindReplaceOptions) { o.Replacement = new(strings.Repeat("x", 65537)) }},
		{"empty find", func(o *FindReplaceOptions) { o.Find = "" }},
		{"large find", func(o *FindReplaceOptions) { o.Find = strings.Repeat("x", 4097) }},
		{"invalid find utf8", func(o *FindReplaceOptions) { o.Find = string([]byte{0xff}) }},
		{"invalid mode", func(o *FindReplaceOptions) { o.Mode = "fuzzy" }},
		{"invalid regex", func(o *FindReplaceOptions) { o.Mode, o.Find = "regex", "[" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := valid
			tc.change(&opts)
			if err := opts.Validate(); err == nil {
				t.Fatal("invalid transform accepted")
			}
		})
	}
}

func TestFindReplaceLiteralDollarsCaseNullAndOptions(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,artist,path) VALUES(1,'MIX mix','Mix Artist','one.mp3')"); err != nil {
		t.Fatal(err)
	}
	opts := FindReplaceOptions{Identifiers: []string{"1"}, Fields: []string{"title", "album"}, Find: "mix", Replacement: new("$1"), MatchCase: true}
	plan, err := PreviewFindReplace(context.Background(), d, opts)
	if err != nil || len(plan.Tracks) != 1 || len(plan.Tracks[0].Changes) != 1 || plan.Tracks[0].Changes[0].After != "MIX $1" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	opts.MatchCase = false
	if _, err := ApplyFindReplace(context.Background(), d, opts); err == nil {
		t.Fatal("changed matching options reused a review")
	}
	opts.MatchCase = true
	if _, err := ApplyFindReplace(context.Background(), d, opts); err != nil {
		t.Fatal(err)
	}
	tr, err := db.GetTrackByID(context.Background(), d, 1)
	if err != nil || tr.Title.String != "MIX $1" || tr.Album.Valid {
		t.Fatalf("literal replacement or NULL preservation failed: %+v %v", tr, err)
	}
	opts.Identifiers = []string{"999"}
	if _, err := PreviewFindReplace(context.Background(), d, opts); err == nil {
		t.Fatal("missing track accepted")
	}
}

func TestFindReplaceReviewApplyAndStale(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,artist,path,rating) VALUES(1,'Mix One','MIX Artist','one.mp3',80),(2,'Mix Two','Other','two.mp3',60)"); err != nil {
		t.Fatal(err)
	}
	opts := FindReplaceOptions{Identifiers: []string{"2", "1", "one.mp3"}, Fields: []string{"title", "artist"}, Find: "mix", Replacement: new("Set")}
	plan, err := PreviewFindReplace(context.Background(), d, opts)
	if err != nil || len(plan.Tracks) != 2 || plan.ChangedFields != 3 || !plan.CanApply {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	tr, _ := db.GetTrackByID(context.Background(), d, 1)
	if tr.Title.String != "Mix One" {
		t.Fatal("preview wrote metadata")
	}
	opts.PlanToken = plan.PlanToken
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ApplyFindReplace(ctx, d, opts); err == nil {
		t.Fatal("cancelled apply accepted the reviewed write")
	}
	unchanged, err := db.GetTrackByID(t.Context(), d, 1)
	if err != nil || unchanged.Title.String != "Mix One" || unchanged.Artist.String != "MIX Artist" {
		t.Fatalf("cancelled apply changed metadata: %+v %v", unchanged, err)
	}
	if _, err := d.Exec("UPDATE Track SET title='Changed' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyFindReplace(context.Background(), d, opts); err == nil {
		t.Fatal("accepted stale review")
	}
	plan, err = PreviewFindReplace(context.Background(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	result, err := ApplyFindReplace(context.Background(), d, opts)
	if err != nil || len(result.TrackIDs) != 1 || result.TrackIDs[0] != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	tr, err = db.GetTrackByID(context.Background(), d, 1)
	if err != nil || tr.Title.String != "Set One" || tr.Artist.String != "Set Artist" || tr.Rating.Int64 != 80 || tr.Path.String != "one.mp3" {
		t.Fatalf("track=%+v err=%v", tr, err)
	}
}

func TestFindReplaceNativeMatchingAndCaptureExpansion(t *testing.T) {
	for _, tc := range []struct {
		name, before, find, replacement, mode, after string
	}{
		{"Unicode fold", "Äther mix", "äther", "New", "literal", "New mix"},
		{"literal metacharacters", "a.b and axb", "a.b", "$1", "literal", "$1 and axb"},
		{"named capture and dollar", "DJ Alice", "^DJ (?P<artist>.*)$", "${artist} $$", "regex", "Alice $"},
		{"zero-width match", "Original", "^", "Prefix ", "regex", "Prefix Original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := createTestDB(t)
			if _, err := d.Exec("INSERT INTO Track(id,title,comment,path) VALUES(1,'Track',?,'one.mp3')", tc.before); err != nil {
				t.Fatal(err)
			}
			opts := FindReplaceOptions{Identifiers: []string{"1"}, Fields: []string{"comment"}, Find: tc.find, Replacement: &tc.replacement, Mode: tc.mode}
			plan, err := PreviewFindReplace(t.Context(), d, opts)
			if err != nil || !plan.CanApply || len(plan.Tracks) != 1 || len(plan.Tracks[0].Changes) != 1 || plan.Tracks[0].Changes[0].After != tc.after {
				t.Fatalf("review=%+v error=%v", plan, err)
			}
			opts.PlanToken = plan.PlanToken
			if _, err := ApplyFindReplace(t.Context(), d, opts); err != nil {
				t.Fatal(err)
			}
			tr, err := db.GetTrackByID(t.Context(), d, 1)
			if err != nil || tr.Comment.String != tc.after {
				t.Fatalf("stored=%+v error=%v", tr, err)
			}
		})
	}
}

func TestFindReplaceRegexEmptyAndRollback(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,artist,path) VALUES(1,'One','DJ Alice','one.mp3'),(2,'Two','DJ Bob','two.mp3')"); err != nil {
		t.Fatal(err)
	}
	opts := FindReplaceOptions{Identifiers: []string{"1", "2"}, Fields: []string{"artist"}, Find: "^DJ (.*)$", Mode: "regex", Replacement: new("$1")}
	plan, err := PreviewFindReplace(context.Background(), d, opts)
	if err != nil || len(plan.Tracks) != 2 || len(plan.Tracks[0].Changes) != 1 || plan.Tracks[0].Changes[0].After != "Alice" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := d.Exec("CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN NEW.id=2 BEGIN SELECT RAISE(ABORT, 'reject'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyFindReplace(context.Background(), d, opts); err == nil {
		t.Fatal("ignored rejected second update")
	}
	tr, _ := db.GetTrackByID(context.Background(), d, 1)
	if tr.Artist.String != "DJ Alice" {
		t.Fatal("partial batch escaped rollback")
	}
	opts.Find, opts.Mode, opts.Replacement = "DJ Alice", "literal", new("")
	plan, err = PreviewFindReplace(context.Background(), d, opts)
	if err != nil || len(plan.Tracks) != 2 || len(plan.Tracks[0].Changes) != 1 || plan.Tracks[0].Changes[0].After != "" || !plan.CanApply {
		t.Fatalf("empty optional replacement plan=%+v err=%v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := ApplyFindReplace(t.Context(), d, opts); err != nil {
		t.Fatal(err)
	}
	tr, err = db.GetTrackByID(t.Context(), d, 1)
	if err != nil || tr.Artist.String != "" || !tr.Artist.Valid {
		t.Fatalf("explicit empty replacement was not saved: %+v %v", tr, err)
	}
	opts.Fields, opts.Find = []string{"title"}, "One"
	plan, err = PreviewFindReplace(context.Background(), d, opts)
	if err != nil || plan.CanApply || len(plan.Tracks) != 2 || len(plan.Tracks[0].Changes) != 1 || plan.Tracks[0].Changes[0].ErrorCode != "metadata_title_required" {
		t.Fatalf("empty title plan=%+v err=%v", plan, err)
	}
	opts.Find = "absent"
	plan, err = PreviewFindReplace(context.Background(), d, opts)
	if err != nil || plan.CanApply || plan.ChangedFields != 0 {
		t.Fatalf("no-match plan=%+v err=%v", plan, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PreviewFindReplace(ctx, d, opts); err == nil {
		t.Fatal("ignored cancellation")
	}
}
