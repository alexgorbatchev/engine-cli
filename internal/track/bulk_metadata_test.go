package track

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestBulkMetadataPreservesOtherFields(t *testing.T) {
	d, _, _ := createTestDB(t)
	if _, err := d.Exec("INSERT INTO Track(id,title,artist,path,bpmAnalyzed,rating) VALUES(1,'One','First','one.mp3',123.4,100),(2,'Two','Second','two.mp3',124,60)"); err != nil {
		t.Fatal(err)
	}
	edit := BatchMetadataEdit{Identifiers: []string{"2", "1", "one.mp3"}, MetadataFields: MetadataFields{Artist: new(""), Year: new(int64(0)), RatingStars: new(int64(0)), Key: new(int64(-1))}}
	ids, err := EditMetadataBatch(context.Background(), d, edit)
	if err != nil || len(ids) != 2 || ids[0] != 2 || ids[1] != 1 {
		t.Fatalf("ids=%v error=%v", ids, err)
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
	for _, ids := range [][]string{nil, {""}, make([]string, MaxMetadataBatch+1)} {
		edit := BatchMetadataEdit{Identifiers: ids, MetadataFields: MetadataFields{Artist: new("Artist")}}
		if _, err := EditMetadataBatch(context.Background(), nil, edit); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
}
