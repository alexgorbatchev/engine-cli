package importsource

import (
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestReadRejectsDamagedArchiveTables(t *testing.T) {
	for _, statement := range []string{
		"DROP TABLE Document",
		"DROP TABLE Track",
		"ALTER TABLE Document RENAME TO OldDocument; CREATE TABLE Document(id,xml); INSERT INTO Document SELECT NULL,xml FROM OldDocument",
		"ALTER TABLE Track RENAME TO OldTrack; CREATE TABLE Track(documentId,sourceId,originUUID,originTrackId); INSERT INTO Track SELECT documentId,NULL,originUUID,originTrackId FROM OldTrack",
	} {
		t.Run(statement, func(t *testing.T) {
			library := t.TempDir()
			if err := Replace(t.Context(), library, readArchive(t, library).Revision(), sourceState()); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(companion.ImportsPath(library))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(t.Context(), library); progress.ErrorCode(err) != "import_store_unavailable" {
				t.Fatalf("damaged archive accepted: %v", err)
			}
			if err := Replace(t.Context(), library, sourceState().Revision(), emptyState()); progress.ErrorCode(err) != "import_store_unavailable" {
				t.Fatalf("damaged archive overwritten: %v", err)
			}
		})
	}
}

func TestPublicationFailureRollsBackExactArchive(t *testing.T) {
	for _, event := range []string{"DELETE ON Track", "INSERT ON Document", "INSERT ON Track"} {
		t.Run(event, func(t *testing.T) {
			library := t.TempDir()
			before := sourceState()
			if err := Replace(t.Context(), library, readArchive(t, library).Revision(), before); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(companion.ImportsPath(library))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec("CREATE TRIGGER fail_publication BEFORE " + event + " BEGIN SELECT RAISE(ABORT,'native publication failure'); END"); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			next := sourceState()
			next.Tracks[0].OriginTrackID++
			if err := Replace(t.Context(), library, before.Revision(), next); progress.ErrorCode(err) != "import_store_unavailable" {
				t.Fatalf("failed publication accepted: %v", err)
			}
			if got := readArchive(t, library); got.Revision() != before.Revision() {
				t.Fatalf("failed publication changed exact source or mappings: %+v", got)
			}
		})
	}
}
