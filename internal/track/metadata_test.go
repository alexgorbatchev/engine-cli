package track

import (
	"context"
	"testing"
)

func TestEditMetadata(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,path,bpmAnalyzed,rating) VALUES(1,'Original','song.mp3',123.4,100)"); err != nil {
		t.Fatal(err)
	}
	e := MetadataEdit{Identifier: "1", Title: new("New"), Artist: new("Artist"), Album: new("Album"), Genre: new("House"), Comment: new("Comment"), Label: new("Label"), Composer: new("Composer"), Remixer: new("Remixer"), Year: new(int64(2026)), RatingStars: new(int64(3)), Key: new(int64(20))}
	tr, err := EditMetadata(context.Background(), d, e)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title.String != "New" || tr.Label.String != "Label" || tr.Composer.String != "Composer" || tr.Remixer.String != "Remixer" || tr.Comment.String != "Comment" || tr.Year.Int64 != 2026 || tr.Rating.Int64 != 60 || tr.Key.Int64 != 20 || tr.Path.String != "song.mp3" || tr.BPMAnalyzed.Float64 != 123.4 || !tr.LastEditTime.Valid {
		t.Fatalf("%+v", tr)
	}
	// Key 0 is C major, not a cleared key.
	if tr, err = EditMetadata(context.Background(), d, MetadataEdit{Identifier: "1", Key: new(int64(0))}); err != nil || !tr.Key.Valid || tr.Key.Int64 != 0 {
		t.Fatalf("%+v %v", tr, err)
	}
	tr, err = EditMetadata(context.Background(), d, MetadataEdit{Identifier: "1", Artist: new(""), Year: new(int64(0)), RatingStars: new(int64(0)), Key: new(int64(NoKey))})
	if err != nil || tr.Artist.String != "" || tr.Year.Int64 != 0 || tr.Rating.Int64 != 0 || tr.Key.Valid || tr.Album.String != "Album" {
		t.Fatalf("%+v %v", tr, err)
	}
	for _, edit := range []MetadataEdit{{}, {Identifier: "1"}, {Identifier: "1", Year: new(int64(10000))}, {Identifier: "1", RatingStars: new(int64(-1))}, {Identifier: "1", Key: new(int64(24))}, {Identifier: "1", Key: new(int64(-2))}, {Identifier: "1", Title: new(" ")}, {Identifier: "999", Title: new("Missing")}} {
		if _, err := EditMetadata(context.Background(), d, edit); err == nil {
			t.Fatalf("accepted %+v", edit)
		}
	}
	if _, err := d.Exec("CREATE TRIGGER reject_metadata BEFORE UPDATE ON Track BEGIN SELECT RAISE(ABORT, 'reject'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := EditMetadata(context.Background(), d, e); err == nil {
		t.Fatal("ignored write failure")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := EditMetadata(context.Background(), d, e); err == nil {
		t.Fatal("ignored database failure")
	}
}
