package track

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestBulkMetadataPreservesOtherFields(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,artist,path,bpmAnalyzed,rating) VALUES(1,'One','First','one.mp3',123.4,100),(2,'Two','Second','two.mp3',124,60)"); err != nil {
		t.Fatal(err)
	}
	edit := BatchMetadataEdit{Identifiers: []string{"2", "1", "one.mp3"}, MetadataFields: MetadataFields{Artist: new(""), Year: new(int64(0)), RatingStars: new(int64(0)), Key: new(int64(-1))}}
	result, err := EditMetadataBatch(context.Background(), d, edit)
	if err != nil || !reflect.DeepEqual(result.TrackIDs, []int64{2, 1}) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	tr, err := db.GetTrackByID(context.Background(), d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title.String != "One" || tr.Artist.String != "" || tr.Rating.Int64 != 0 || tr.Year.Int64 != 0 || tr.Key.Valid || tr.Path.String != "one.mp3" || tr.BPMAnalyzed.Float64 != 123.4 {
		t.Fatalf("track=%+v", tr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EditMetadataBatch(ctx, d, edit); err == nil {
		t.Fatal("accepted cancelled operation")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := EditMetadataBatch(context.Background(), d, edit); err == nil {
		t.Fatal("accepted closed database")
	}
}

func TestBulkMetadataRejectsInvalidSelection(t *testing.T) {
	for _, edit := range []BatchMetadataEdit{
		{Identifiers: nil, MetadataFields: MetadataFields{Artist: new("Artist")}},
		{Identifiers: []string{""}, MetadataFields: MetadataFields{Artist: new("Artist")}},
		{Identifiers: make([]string, MaxMetadataBatch+1), MetadataFields: MetadataFields{Artist: new("Artist")}},
		{Identifiers: []string{"1"}, MetadataFields: MetadataFields{Artist: new("Artist")}, DryRun: true, PlanToken: "reviewed"},
	} {
		if _, err := EditMetadataBatch(context.Background(), nil, edit); err == nil {
			t.Fatalf("accepted %+v", edit)
		}
		if _, err := PreviewMetadataBatch(context.Background(), nil, edit); err == nil {
			t.Fatalf("previewed %+v", edit)
		}
	}
	if _, err := EditMetadataBatch(context.Background(), nil, BatchMetadataEdit{Identifiers: []string{"1"}, MetadataFields: MetadataFields{Artist: new("Artist")}, DryRun: true}); err == nil {
		t.Fatal("applied a dry run")
	}
}

// A review lists each track's changed fields with before and after values, binds them with a plan token, and an
// apply with that token writes exactly the reviewed changes, refusing once the tracks or the edit change.
func TestBatchEditReviewBindsTheApply(t *testing.T) {
	ctx := context.Background()
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,filename,artist,path,year,rating,key,lastEditTime) VALUES(1,'One','one.mp3','First','one.mp3',2020,100,5,'reviewed'),(2,'','Two Mix.mp3','Together','two.mp3',NULL,0,NULL,'untouched')"); err != nil {
		t.Fatal(err)
	}
	edit := BatchMetadataEdit{Identifiers: []string{"1", "2", "1"}, MetadataFields: MetadataFields{Artist: new("Together"), Year: new(int64(0)), RatingStars: new(int64(3)), Key: new(int64(-1))}}
	review := edit
	review.DryRun = true
	plan, err := PreviewMetadataBatch(ctx, d, review)
	if err != nil {
		t.Fatal(err)
	}
	want := []TrackEdit{
		{TrackID: 1, Title: "One", Changes: []FieldChange{{"artist", "First", "Together"}, {"year", int64(2020), nil}, {"rating_stars", int64(5), int64(3)}, {"key", int64(5), nil}}},
		{TrackID: 2, Title: "Two Mix", Changes: []FieldChange{{"rating_stars", nil, int64(3)}}},
	}
	if !reflect.DeepEqual(plan.Tracks, want) || plan.SelectedTracks != 2 || plan.ChangedTracks != 2 || plan.ChangedFields != 5 || !plan.CanApply || plan.PlanToken == "" || len(plan.Unresolved) != 0 {
		t.Fatalf("plan=%+v", plan)
	}
	encoded, err := json.Marshal(plan)
	if err != nil || !strings.Contains(string(encoded), `{"field":"year","before":2020,"after":null}`) || !strings.Contains(string(encoded), `"unresolved":[]`) {
		t.Fatalf("review JSON %s: %v", encoded, err)
	}
	var artist string
	if err := d.QueryRow("SELECT artist FROM Track WHERE id=1").Scan(&artist); err != nil || artist != "First" {
		t.Fatalf("review wrote %q: %v", artist, err)
	}
	apply := edit
	apply.PlanToken = plan.PlanToken
	if err := CheckMetadataBatch(ctx, d, apply); err != nil {
		t.Fatal(err)
	}
	result, err := EditMetadataBatch(ctx, d, apply)
	if err != nil || !reflect.DeepEqual(result.TrackIDs, []int64{1, 2}) || result.ChangedFields != 5 || !reflect.DeepEqual(result.Tracks, want) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	var year, key any
	var rating int64
	if err := d.QueryRow("SELECT year,rating,key FROM Track WHERE id=1").Scan(&year, &rating, &key); err != nil || year != int64(0) || rating != 60 || key != nil {
		t.Fatalf("written year=%v rating=%d key=%v: %v", year, rating, key, err)
	}
	// The same token no longer matches the tracks; a fresh review finds nothing left to change.
	if err := CheckMetadataBatch(ctx, d, apply); progress.ErrorCode(err) != "metadata_edit_stale" {
		t.Fatalf("check with stale token: %v", err)
	}
	if _, err := EditMetadataBatch(ctx, d, apply); progress.ErrorCode(err) != "metadata_edit_stale" {
		t.Fatalf("apply with stale token: %v", err)
	}
	again, err := PreviewMetadataBatch(ctx, d, review)
	if err != nil || again.CanApply || again.ChangedFields != 0 || again.ChangedTracks != 0 {
		t.Fatalf("unchanged review=%+v %v", again, err)
	}
	apply.PlanToken = again.PlanToken
	if _, err := EditMetadataBatch(ctx, d, apply); progress.ErrorCode(err) != "metadata_edit_unchanged" {
		t.Fatalf("applied an unchanged review: %v", err)
	}
	// An in-place apply without a token writes at once, and only the tracks it changes.
	apply.PlanToken = ""
	apply.MetadataFields = MetadataFields{Album: new("Album")}
	if _, err := d.Exec("UPDATE Track SET album='Album', lastEditTime='untouched' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	result, err = EditMetadataBatch(ctx, d, apply)
	if err != nil || !reflect.DeepEqual(result.TrackIDs, []int64{1}) {
		t.Fatalf("in-place result=%+v %v", result, err)
	}
	var edited string
	if err := d.QueryRow("SELECT lastEditTime FROM Track WHERE id=2").Scan(&edited); err != nil || edited != "untouched" {
		t.Fatalf("unchanged track rewritten: %q %v", edited, err)
	}
}

func TestBatchEditReportsTracksItCannotFind(t *testing.T) {
	ctx := context.Background()
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(1,'One','one.mp3')"); err != nil {
		t.Fatal(err)
	}
	edit := BatchMetadataEdit{Identifiers: []string{"1", "999", "missing.mp3"}, MetadataFields: MetadataFields{Genre: new("House")}}
	review := edit
	review.DryRun = true
	plan, err := PreviewMetadataBatch(ctx, d, review)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanApply || !reflect.DeepEqual(plan.Unresolved, []UnresolvedTrack{{"999", "not_found"}, {"missing.mp3", "not_found"}}) || plan.ChangedTracks != 1 {
		t.Fatalf("plan=%+v", plan)
	}
	for _, token := range []string{"", plan.PlanToken} {
		edit.PlanToken = token
		if err := CheckMetadataBatch(ctx, d, edit); progress.ErrorCode(err) != "not_found" {
			t.Fatalf("checked a missing track with token %q: %v", token, err)
		}
		if _, err := EditMetadataBatch(ctx, d, edit); progress.ErrorCode(err) != "not_found" {
			t.Fatalf("applied a missing track with token %q: %v", token, err)
		}
	}
	var genre any
	if err := d.QueryRow("SELECT genre FROM Track WHERE id=1").Scan(&genre); err != nil || genre != nil {
		t.Fatalf("refused apply wrote %v: %v", genre, err)
	}
}
