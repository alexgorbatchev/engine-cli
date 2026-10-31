package rekordbox

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestFinishImportExposesReceiptOnlyAfterNativeAndArchivePublication(t *testing.T) {
	for _, outcome := range []string{"published", "closed transaction", "archive failure"} {
		t.Run(outcome, func(t *testing.T) {
			root, d, state := publicationFixture(t)
			before, err := importsource.Read(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "archive failure" {
				if err := importsource.Replace(t.Context(), root, before.Revision(), state); err != nil {
					t.Fatal(err)
				}
				before, err = importsource.Read(t.Context(), root)
				if err != nil {
					t.Fatal(err)
				}
				archive, err := db.Open(companion.ImportsPath(root))
				if err != nil {
					t.Fatal(err)
				}
				_, err = archive.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON Document BEGIN SELECT RAISE(ABORT,'receipt publication rejected'); END`)
				closeErr := archive.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("setting publication failure: %v, close: %v", err, closeErr)
				}
			}
			src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Name="Imported document" Location="file:///original.wav"/><TRACK TrackID="2" Name="New track" Location="file:///new.wav"/><TRACK TrackID="3" Location="file:///missing.wav"/></COLLECTION></DJ_PLAYLISTS>`))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareTrack(src.xml.Collection.Tracks[1], src.library.Songs[1], audioExtent{rate: 48000, frames: 480000})
			if err != nil {
				t.Fatal(err)
			}
			var receipt Result
			_, err = history.Run(t.Context(), root, "migrate.import", history.ScopeCollection, nil, func(ctx context.Context) error {
				tx, err := d.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				track, err := readWrittenTrack(ctx, tx, 1)
				if err != nil {
					return err
				}
				plan := nativePlan{source: src, name: "Imported root", archive: before, tracks: map[string]trackDisposition{"id:1": {retained: track}, "id:2": {prepared: &prepared}, "id:3": {omit: true}}}
				native, err := writeReviewed(ctx, tx, plan)
				if err != nil {
					return err
				}
				if outcome == "closed transaction" {
					if err := tx.Rollback(); err != nil {
						return err
					}
				}
				receipt, err = finishImport(ctx, tx, importCompletion{publication: archivePublication{library: root, revision: before.Revision(), archive: native.archive}, documentID: src.document.ID, native: native})
				return err
			})
			if outcome != "published" {
				if err == nil || receipt != (Result{}) {
					t.Fatalf("failed publication exposed successful receipt: %+v, %v", receipt, err)
				}
				return
			}
			if err != nil || receipt.RootID <= 0 || receipt.DocumentID != src.document.ID || receipt.CreatedCount != 1 || receipt.ReusedCount != 1 || receipt.SkippedCount != 1 {
				t.Fatalf("committed native result projection: %+v, %v", receipt, err)
			}
			published, err := importsource.Read(t.Context(), root)
			if err != nil || len(published.Documents) != 1 || len(published.Tracks) != 2 {
				t.Fatalf("receipt preceded preserved evidence: %+v, %v", published, err)
			}
		})
	}
}
