package track

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// trackInfo reads a track's own artist, genre and year.
func trackInfo(t *testing.T, d *sql.DB, id int) (string, string, int64) {
	t.Helper()
	var artist, genre sql.NullString
	var year sql.NullInt64
	if err := d.QueryRow(`SELECT artist, genre, year FROM Track WHERE id=?`, id).Scan(&artist, &genre, &year); err != nil {
		t.Fatal(err)
	}
	return artist.String, genre.String, year.Int64
}

func TestTagEditsTakeFileValuesIntoTrackInfo(t *testing.T) {
	f := newTagEditFixture(t)
	ctx := context.Background()
	if _, err := f.d.Exec(`UPDATE Track SET artist='Artist' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	edits := []TagEdit{
		// A takes artist and the date's year from its file and writes a new title into the file.
		{Identifier: "1", Title: text("New a"), Take: []string{"artist", "date"}},
		// B already shows the file's artist, so taking it changes nothing.
		{Identifier: "2", Take: []string{"artist"}},
		// C only takes, without writing the file.
		{Identifier: "3", Take: []string{"genre"}},
	}
	plan, err := PreviewTagEdits(ctx, f.d, edits, f.options())
	if err != nil {
		t.Fatal(err)
	}
	if plan.ChangedFiles != 1 || plan.ChangedTracks != 2 || !plan.CanApply {
		t.Fatalf("plan %+v", plan)
	}
	want := []TagChange{{Field: "artist", Before: "", After: "Artist"}, {Field: "year", Before: "", After: "2020"}}
	if got := plan.Files[0].TrackChanges; !slices.Equal(got, want) {
		t.Fatalf("track changes %+v", got)
	}
	if len(plan.Files[1].TrackChanges) != 0 || len(plan.Files[2].Changes) != 0 {
		t.Fatalf("files %+v", plan.Files)
	}
	if artist, _, _ := trackInfo(t, f.d, 1); artist != "" {
		t.Fatal("the review changed track info")
	}
	result, err := ApplyTagEdits(ctx, f.d, edits, plan.PlanToken, f.options())
	if err != nil || result.Succeeded != 1 || result.UpdatedTracks != 2 || !slices.Equal(result.TrackIDs, []int64{1, 3}) {
		t.Fatalf("result %+v %v", result, err)
	}
	if artist, _, year := trackInfo(t, f.d, 1); artist != "Artist" || year != 2020 {
		t.Fatalf("track 1 took %q %d", artist, year)
	}
	if _, genre, _ := trackInfo(t, f.d, 3); genre != "House" {
		t.Fatalf("track 3 took genre %q", genre)
	}
	// Track info that changed after the review makes its token stale.
	again, err := PreviewTagEdits(ctx, f.d, []TagEdit{{Identifier: "3", Take: []string{"genre", "comment"}}}, f.options())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Exec(`UPDATE Track SET comment='edited elsewhere' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	if err := CheckTagEdits(ctx, f.d, []TagEdit{{Identifier: "3", Take: []string{"genre", "comment"}}}, again.PlanToken, f.options()); err == nil {
		t.Fatal("a changed track kept the token")
	}
}

func TestTagEditsTakeRefusesBadRequestsAndDates(t *testing.T) {
	f := newTagEditFixture(t)
	for _, edit := range []TagEdit{
		{Identifier: "1", Take: []string{"label"}},
		{Identifier: "1", Take: []string{"artist", "artist"}},
		{Identifier: "1", Artist: text("Both"), Take: []string{"artist"}},
	} {
		if err := ValidateTagEdits([]TagEdit{edit}); err == nil {
			t.Fatalf("accepted %+v", edit)
		}
	}
	// A file date Engine can't read as a year is that file's error, not a guess.
	inspection := f.tags["a.mp3"]
	inspection.Date = "May 2020"
	f.tags["a.mp3"] = inspection
	plan, err := PreviewTagEdits(context.Background(), f.d, []TagEdit{{Identifier: "1", Take: []string{"date"}}}, f.options())
	if err != nil || plan.Files[0].ErrorCode != "invalid_tag_date" || plan.CanApply {
		t.Fatalf("plan %+v %v", plan, err)
	}
}
