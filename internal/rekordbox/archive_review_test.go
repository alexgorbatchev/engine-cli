package rekordbox

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestArchiveReviewUsesExtantNativeOriginsRatherThanDocumentPresence(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"unseen", "document only", "partial", "complete", "deleted", "different document", "moved media path", "reassigned local row", "unknown source selector"} {
		t.Run(name, func(t *testing.T) {
			d := importDatabase(t)
			src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="0" Location="file:///source/a.wav"/><TRACK TrackID="2" Location="file:///source/b.wav"/></COLLECTION></DJ_PLAYLISTS>`))
			if err != nil {
				t.Fatal(err)
			}
			state := importsource.State{}
			if name != "unseen" {
				state.Documents = []importsource.Document{src.document}
			}
			ids := make([]int64, 2)
			for i, path := range []string{"/native/a.wav", "/native/b.wav"} {
				ids[i], err = db.InsertTrack(ctx, d, &db.Track{Path: sql.NullString{String: path, Valid: true}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if name != "unseen" && name != "document only" {
				state.Tracks = append(state.Tracks, archivedMapping(t, d, src.document.ID, src.selectors[0], ids[0]))
			}
			if name == "complete" || name == "deleted" || name == "moved media path" {
				state.Tracks = append(state.Tracks, archivedMapping(t, d, src.document.ID, src.selectors[1], ids[1]))
			}
			if name == "different document" {
				state.Tracks[0].DocumentID = "another document"
			}
			if name == "unknown source selector" {
				state.Tracks[0].SourceID = "id:999"
			}
			if name == "reassigned local row" {
				if _, err := d.ExecContext(ctx, "UPDATE Track SET originDatabaseUuid='replacement-origin' WHERE id=?", ids[0]); err != nil {
					t.Fatal(err)
				}
			}
			if name == "deleted" {
				if _, err := d.ExecContext(ctx, "DELETE FROM Track WHERE id=?", ids[1]); err != nil {
					t.Fatal(err)
				}
			}
			if name == "moved media path" {
				if _, err := d.ExecContext(ctx, "UPDATE Track SET path='changed.wav' WHERE id=?", ids[1]); err != nil {
					t.Fatal(err)
				}
			}
			got, err := reviewArchive(ctx, d, src, state)
			if err != nil {
				t.Fatal(err)
			}
			wantComplete := name == "complete" || name == "moved media path"
			wantStale := name == "deleted" || name == "reassigned local row" || name == "unknown source selector"
			if got.complete != wantComplete || (len(got.stale) > 0) != wantStale {
				t.Fatalf("archive completeness/stale evidence: %+v", got)
			}
			if name == "partial" && (len(got.tracks) != 1 || got.tracks["id:0"].id != ids[0]) {
				t.Fatalf("partial native identity lost: %+v", got)
			}
			if (name == "unseen" || name == "document only" || name == "different document") && len(got.tracks) != 0 {
				t.Fatalf("document presence invented mappings: %+v", got)
			}
		})
	}
}

func TestArchiveReviewEmptyDocumentRequiresRecordedArchive(t *testing.T) {
	d := importDatabase(t)
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION/></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, present := range []bool{false, true} {
		state := importsource.State{}
		if present {
			state.Documents = []importsource.Document{src.document}
		}
		got, err := reviewArchive(context.Background(), d, src, state)
		if err != nil || got.complete != present {
			t.Fatalf("empty archive presence=%v: %+v, %v", present, got, err)
		}
	}
	if _, err := d.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewArchive(context.Background(), d, src, importsource.State{}); err == nil {
		t.Fatal("failed collection read accepted as empty collection")
	}
}

func archivedMapping(t *testing.T, d *sql.DB, document, selector string, id int64) importsource.Track {
	t.Helper()
	mapping := importsource.Track{DocumentID: document, SourceID: selector}
	if err := d.QueryRow("SELECT originDatabaseUuid,originTrackId FROM Track WHERE id=?", id).Scan(&mapping.OriginUUID, &mapping.OriginTrackID); err != nil {
		t.Fatal(err)
	}
	return mapping
}
