package rekordbox

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestWriteCurrentReviewRechecksNativeEvidenceBeforeInsertion(t *testing.T) {
	ctx := context.Background()
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unchanged", "library identity", "native path", "native origin", "new native track", "new root"} {
		t.Run(change, func(t *testing.T) {
			d := importDatabase(t)
			id, err := db.InsertTrack(ctx, d, &db.Track{Path: text("/a.wav"), Title: text("Curated")})
			if err != nil {
				t.Fatal(err)
			}
			tracks, err := readNativeTracks(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			uuid, err := destinationIdentity(ctx, d, "Imported")
			if err != nil {
				t.Fatal(err)
			}
			archive := importsource.State{}
			in := reviewInput{source: src, options: Options{Name: "Imported", Conflicts: policyReuse}, libraryUUID: uuid, libraryPath: "/library", archive: archive, entries: []Entry{{SourceID: "id:1", Status: statusExisting, TrackID: id}}, native: tracks}
			review, err := buildReview(in)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "library identity":
				_, err = d.Exec(`UPDATE Information SET uuid='different' WHERE id=1`)
			case "native path":
				_, err = d.Exec(`UPDATE Track SET path='/moved.wav' WHERE id=?`, id)
			case "native origin":
				_, err = d.Exec(`UPDATE Track SET originTrackId=originTrackId+100 WHERE id=?`, id)
			case "new native track":
				_, err = db.InsertTrack(ctx, d, &db.Track{Path: text("/other.wav")})
			case "new root":
				_, err = db.CreatePlaylist(ctx, d, "Imported", 0, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			tx, err := d.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			got, err := writeCurrentReview(ctx, tx, reviewedWrite{input: in, archive: archive, token: review.PlanToken})
			if change == "unchanged" {
				if err != nil || got.rootID <= 0 || got.reused != 1 || got.created != 0 {
					t.Fatalf("unchanged review failed: %+v, %v", got, err)
				}
			} else if err == nil || got.rootID != 0 {
				t.Fatalf("changed native evidence accepted: %+v, %v", got, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var roots int
			if err := d.QueryRow(`SELECT COUNT(*) FROM Playlist`).Scan(&roots); err != nil {
				t.Fatal(err)
			}
			want := 0
			if change == "new root" {
				want = 1
			}
			if roots != want {
				t.Fatalf("caller rollback did not retain transaction ownership: got %d roots, want %d", roots, want)
			}
		})
	}
}
