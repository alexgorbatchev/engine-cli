package track

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestCapitalizationRejectsInvalidInputBeforeStorage(t *testing.T) {
	invalid := CapitalizationOptions{Identifiers: []string{"1"}, Fields: []string{"path"}, Recipe: "upper"}
	if _, err := PreviewCapitalization(t.Context(), nil, invalid); err == nil {
		t.Fatal("invalid review reached storage")
	}
	if _, err := ApplyCapitalization(t.Context(), nil, invalid); err == nil {
		t.Fatal("invalid apply reached storage")
	}
	if _, err := CheckCapitalization(t.Context(), nil, invalid); err == nil {
		t.Fatal("invalid preflight reached storage")
	}
	for _, tc := range []struct{ recipe, language, source string }{
		{"upper", "", string([]byte{0xff})},
		{"upper", "", strings.Repeat("x", transformFieldBytes+1)},
		{"upper", strings.Repeat("a", 129), "x"},
		{"invalid", "", "x"},
	} {
		if _, err := (CapitalizationOptions{Recipe: tc.recipe, Language: tc.language}).replace(t.Context(), tc.source, transformFieldBytes); err == nil {
			t.Fatal("invalid source or casing options accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (CapitalizationOptions{Recipe: "upper"}).replace(ctx, "x", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled casing continued: %v", err)
	}
}

func TestCapitalizationContextAndOutputBudget(t *testing.T) {
	for _, tc := range []struct {
		name, recipe, source, want string
		remaining                  int
		tooLarge                   bool
	}{
		{"final sigma", "lower", "ΟΣ", "ος", 4, false},
		{"long word context", "lower", strings.Repeat("Ο", 4096) + "Σ", strings.Repeat("ο", 4096) + "ς", 8194, false},
		{"expansion exact budget", "upper", strings.Repeat("ΐ", 100), strings.Repeat("Ϊ́", 100), 600, false},
		{"expansion exceeds budget", "upper", strings.Repeat("ΐ", 100), "", 599, true},
		{"empty field at zero budget", "upper", "", "", 0, false},
		{"nonempty field at zero budget", "upper", "a", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := CapitalizationOptions{Recipe: tc.recipe}
			got, err := opts.replace(t.Context(), tc.source, tc.remaining)
			if tc.tooLarge {
				if progress.ErrorCode(err) != "metadata_transform_too_large" {
					t.Fatalf("expected output budget rejection: %q %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("capitalization=%q want=%q error=%v", got, tc.want, err)
			}
		})
	}
}

func TestCapitalizationUnicodeReviewAndApply(t *testing.T) {
	for _, tc := range []struct{ name, recipe, language, before, after string }{
		{"uppercase expansion", "upper", "", "straße café", "STRASSE CAFÉ"},
		{"Greek final sigma", "lower", "", "ΟΣ", "ος"},
		{"Turkish uppercase", "upper", "tr", "istanbul ı", "İSTANBUL I"},
		{"Dutch title", "title", "nl", "ijsselmeer MIX", "IJsselmeer Mix"},
		{"Unicode title", "title", "", "élan VITAL", "Élan Vital"},
		{"spacing preserved", "upper", "", "  café\tremix\n", "  CAFÉ\tREMIX\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := createTestDB(t)
			if _, err := d.Exec("INSERT INTO Track(id,title,artist,album,comment,path,rating) VALUES(1,'TRACK',?,NULL,'','one.mp3',80)", tc.before); err != nil {
				t.Fatal(err)
			}
			opts := CapitalizationOptions{Identifiers: []string{"1", "one.mp3"}, Fields: []string{"artist", "album", "comment"}, Recipe: tc.recipe, Language: tc.language}
			plan, err := PreviewCapitalization(t.Context(), d, opts)
			if err != nil || plan.SelectedTracks != 1 || plan.ChangedFields != 1 || !plan.CanApply || plan.Tracks[0].Changes[0].After != tc.after {
				t.Fatalf("plan=%+v error=%v", plan, err)
			}
			before, err := db.GetTrackByID(t.Context(), d, 1)
			if err != nil || before.Artist.String != tc.before {
				t.Fatalf("preview wrote: %+v %v", before, err)
			}
			opts.PlanToken = plan.PlanToken
			result, err := ApplyCapitalization(t.Context(), d, opts)
			if err != nil || len(result.TrackIDs) != 1 || result.ChangedFields != 1 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			after, err := db.GetTrackByID(t.Context(), d, 1)
			if err != nil || after.Artist.String != tc.after || after.Album.Valid || !after.Comment.Valid || after.Comment.String != "" || after.Rating.Int64 != 80 || after.Title.String != "TRACK" {
				t.Fatalf("stored=%+v error=%v", after, err)
			}
		})
	}
}

func TestCapitalizationConsentCancellationAndRollback(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(1,'first','one.mp3'),(2,'second','two.mp3')"); err != nil {
		t.Fatal(err)
	}
	opts := CapitalizationOptions{Identifiers: []string{"1", "2"}, Fields: []string{"title"}, Recipe: "upper"}
	plan, err := PreviewCapitalization(t.Context(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCapitalization(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("unreviewed apply: %v", err)
	}
	opts.PlanToken, opts.Recipe = plan.PlanToken, "title"
	if _, err := ApplyCapitalization(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("changed recipe: %v", err)
	}
	opts.Recipe, opts.Language = "upper", "tr"
	if _, err := ApplyCapitalization(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("changed language: %v", err)
	}
	opts.Language = ""
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ApplyCapitalization(ctx, d, opts); err == nil {
		t.Fatal("cancelled apply accepted")
	}
	if _, err := d.Exec("CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN NEW.id=2 BEGIN SELECT RAISE(ABORT,'reject'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCapitalization(t.Context(), d, opts); err == nil {
		t.Fatal("failed second update accepted")
	}
	tr, err := db.GetTrackByID(t.Context(), d, 1)
	if err != nil || tr.Title.String != "first" {
		t.Fatalf("partial write: %+v %v", tr, err)
	}
	if _, err := d.Exec("DROP TRIGGER reject_second; UPDATE Track SET artist='externally edited' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckCapitalization(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("changed evidence accepted: %v", err)
	}
}

func TestCapitalizationNoOpValidationAndBounds(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(1,'ALREADY UPPER','one.mp3')"); err != nil {
		t.Fatal(err)
	}
	opts := CapitalizationOptions{Identifiers: []string{"1"}, Fields: []string{"title"}, Recipe: "upper"}
	plan, err := PreviewCapitalization(t.Context(), d, opts)
	if err != nil || plan.CanApply || plan.ChangedFields != 0 {
		t.Fatalf("no-op=%+v error=%v", plan, err)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := CheckCapitalization(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_unavailable" {
		t.Fatalf("no-op accepted: %v", err)
	}
	for _, change := range []func(*CapitalizationOptions){
		func(o *CapitalizationOptions) { o.Identifiers = nil },
		func(o *CapitalizationOptions) { o.Fields = []string{"path"} },
		func(o *CapitalizationOptions) { o.Recipe = "sentence" },
		func(o *CapitalizationOptions) { o.Language = "not_a_language!!!" },
	} {
		invalid := opts
		change(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("invalid capitalization accepted")
		}
	}
	// A full Unicode mapping expands without a replacement template.
	if _, err := d.Exec("UPDATE Track SET title=? WHERE id=1", strings.Repeat("ΐ", transformFieldBytes/3)); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewCapitalization(t.Context(), d, opts); progress.ErrorCode(err) != "metadata_transform_too_large" {
		t.Fatalf("expanded oversized field accepted: %v", err)
	}
}
