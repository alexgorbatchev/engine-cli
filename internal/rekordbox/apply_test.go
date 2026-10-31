package rekordbox

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestWriteReviewedComposesNewRetainedAndOmittedTracksAtomically(t *testing.T) {
	ctx := context.Background()
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Name="Original XML name" Location="file:///source/a.wav"/><TRACK TrackID="2" Name="New" Location="file:///source/b.wav"/><TRACK TrackID="3" Location="file:///source/missing.wav"/></COLLECTION><PLAYLISTS><NODE Type="0" Name="ROOT"><NODE Type="1" Name="Order" KeyType="0"><TRACK Key="2"/><TRACK Key="3"/><TRACK Key="1"/><TRACK Key="2"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareTrack(src.xml.Collection.Tracks[1], src.library.Songs[1], audioExtent{rate: 48000, frames: 480000, bytes: 960000})
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "hierarchy failure"}[fail], func(t *testing.T) {
			d := importDatabase(t)
			id, err := db.InsertTrack(ctx, d, &db.Track{Path: text("/moved/retained.wav"), Title: text("Retained curated name")})
			if err != nil {
				t.Fatal(err)
			}
			mapping := archivedMapping(t, d, src.document.ID, "id:1", id)
			retained := nativeTrack{id: id, path: "/moved/retained.wav", origin: nativeOrigin{uuid: mapping.OriginUUID, id: mapping.OriginTrackID}}
			state := importsource.State{Documents: []importsource.Document{src.document}, Tracks: []importsource.Track{mapping}}
			if fail {
				if _, err := d.Exec(`CREATE TRIGGER reject_order BEFORE INSERT ON Playlist WHEN NEW.title='Order' BEGIN SELECT RAISE(ABORT,'fixture hierarchy failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := d.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			plan := nativePlan{source: src, name: "Reviewed root", archive: state, tracks: map[string]trackDisposition{"id:1": {retained: retained}, "id:2": {prepared: &prepared}, "id:3": {omit: true}}}
			got, err := writeReviewed(ctx, tx, plan)
			if fail {
				if err == nil || got.rootID != 0 {
					t.Fatalf("failed hierarchy returned partial result: %+v, %v", got, err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				var tracks, playlists, performanceRows int
				if err := d.QueryRow(`SELECT COUNT(*) FROM Track`).Scan(&tracks); err != nil {
					t.Fatal(err)
				}
				if err := d.QueryRow(`SELECT COUNT(*) FROM Playlist`).Scan(&playlists); err != nil {
					t.Fatal(err)
				}
				if err := d.QueryRow(`SELECT COUNT(*) FROM PerformanceData`).Scan(&performanceRows); err != nil {
					t.Fatal(err)
				}
				if tracks != 1 || playlists != 0 || performanceRows != 1 {
					t.Fatalf("partial import survived rollback: tracks=%d/playlists=%d/performance=%d", tracks, playlists, performanceRows)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if got.rootID <= 0 || got.created != 1 || got.reused != 1 || got.skipped != 1 || len(got.archive.Tracks) != 2 {
				t.Fatalf("wrong committed dispositions: %+v", got)
			}
			var title string
			if err := d.QueryRow(`SELECT title FROM Track WHERE id=?`, id).Scan(&title); err != nil || title != "Retained curated name" {
				t.Fatalf("reuse overwrote curated metadata: %q, %v", title, err)
			}
			var child int64
			if err := d.QueryRow(`SELECT id FROM Playlist WHERE parentListId=? AND title='Order'`, got.rootID).Scan(&child); err != nil {
				t.Fatal(err)
			}
			members := savedMemberships(t, d, child)
			if len(members) != 2 || members[0] != "New" || members[1] != "Retained curated name" {
				t.Fatalf("reviewed order/dispositions lost: %v", members)
			}
			if len(state.Tracks) != 1 {
				t.Fatal("input archive mutated")
			}
		})
	}
}

func TestWriteReviewedRejectsChangedOrAmbiguousDispositions(t *testing.T) {
	ctx := context.Background()
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///source/a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareTrack(src.xml.Collection.Tracks[0], src.library.Songs[0], audioExtent{rate: 48000, frames: 480000})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "unknown selector", "ambiguous", "retained changed", "retained deleted", "archive conflict", "root collision", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			d := importDatabase(t)
			id, err := db.InsertTrack(ctx, d, &db.Track{Path: text("/native/a.wav")})
			if err != nil {
				t.Fatal(err)
			}
			mapping := archivedMapping(t, d, src.document.ID, "id:1", id)
			retained := nativeTrack{id: id, path: "/native/a.wav", origin: nativeOrigin{uuid: mapping.OriginUUID, id: mapping.OriginTrackID}}
			plan := nativePlan{source: src, name: "Root", tracks: map[string]trackDisposition{"id:1": {retained: retained}}}
			switch name {
			case "missing":
				plan.tracks = nil
			case "unknown selector":
				plan.tracks = map[string]trackDisposition{"id:2": {omit: true}}
			case "ambiguous":
				plan.tracks["id:1"] = trackDisposition{retained: retained, prepared: &prepared}
			case "retained changed":
				if _, err := d.Exec(`UPDATE Track SET path='/changed.wav' WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "retained deleted":
				if _, err := d.Exec(`DELETE FROM Track WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "archive conflict":
				mapping.OriginTrackID += 100
				plan.archive = importsource.State{Documents: []importsource.Document{src.document}, Tracks: []importsource.Track{mapping}}
				plan.tracks["id:1"] = trackDisposition{prepared: &prepared}
			case "root collision":
				if _, err := db.CreatePlaylist(ctx, d, "Root", 0, true); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := d.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run, cancel := context.WithCancel(ctx)
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			got, err := writeReviewed(run, tx, plan)
			if err == nil || got.rootID != 0 || got.archive.Documents != nil {
				t.Fatalf("invalid plan returned partial success: %+v, %v", got, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := d.QueryRow(`SELECT COUNT(*) FROM Track`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 1
			if name == "retained deleted" {
				want = 0
			}
			if count != want {
				t.Fatalf("failed plan leaked track inserts: %d", count)
			}
		})
	}
}
