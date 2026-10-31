package history

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func sourceArchive(t *testing.T, lib string) importsource.State {
	t.Helper()
	s, err := importsource.Read(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func importState(name string) importsource.State {
	xml := []byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="0" Name="` + name + `"/></COLLECTION></DJ_PLAYLISTS>`)
	id := importsource.DocumentID(xml)
	return importsource.State{Documents: []importsource.Document{{ID: id, XML: xml}}, Tracks: []importsource.Track{{DocumentID: id, SourceID: "track:0", OriginUUID: "origin", OriginTrackID: 7}}}
}

func TestImportArchiveUndoRestoresEnrollmentAndExactContent(t *testing.T) {
	for _, enrolled := range []bool{false, true} {
		t.Run(map[bool]string{false: "first import", true: "existing archive"}[enrolled], func(t *testing.T) {
			lib, d, _ := fixture(t)
			if enrolled {
				if err := importsource.Replace(t.Context(), lib, sourceArchive(t, lib).Revision(), importState("Original source")); err != nil {
					t.Fatal(err)
				}
			}
			before := sourceArchive(t, lib)
			entry, err := Run(t.Context(), lib, "migrate.import", ScopeCollection, nil, func(ctx context.Context) error {
				if _, err := d.ExecContext(ctx, "UPDATE Track SET title='Imported metadata'"); err != nil {
					return err
				}
				return importsource.Replace(ctx, lib, before.Revision(), importState("Exact new source"))
			})
			if err != nil || sourceArchive(t, lib).Revision() == before.Revision() {
				t.Fatalf("import history failed: %+v %v", entry, err)
			}
			if _, err := Undo(t.Context(), lib, entry.ID, UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if sourceArchive(t, lib).Revision() != before.Revision() {
				t.Fatal("undo lost original source XML or native mapping")
			}
			if !enrolled {
				if _, err := os.Stat(companion.ImportsPath(lib)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("first-import undo retained archive: %v", err)
				}
			}
			var title string
			if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Original" {
				t.Fatalf("Engine before-image lost: %q %v", title, err)
			}
		})
	}
}

func TestImportArchivePublicationFailureRetainsRecovery(t *testing.T) {
	lib, d, _ := fixture(t)
	if err := importsource.Replace(t.Context(), lib, sourceArchive(t, lib).Revision(), importState("Original source")); err != nil {
		t.Fatal(err)
	}
	archive, err := db.Open(companion.ImportsPath(lib))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Exec("CREATE TRIGGER reject_import BEFORE INSERT ON Document BEGIN SELECT RAISE(ABORT,'native archive publication refused'); END"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	before := sourceArchive(t, lib)
	entry, err := Run(t.Context(), lib, "migrate.import", ScopeCollection, nil, func(ctx context.Context) error {
		if _, err := d.ExecContext(ctx, "UPDATE Track SET title='Published Engine half'"); err != nil {
			return err
		}
		err := importsource.Replace(ctx, lib, before.Revision(), importState("Rejected new source"))
		if err == nil {
			t.Fatal("native archive rejection did not run")
		}
		return errors.Join(err, RequireRecovery(ctx))
	})
	if progress.ErrorCode(err) != "history_recovery_required" || entry == nil || entry.After != nil {
		t.Fatalf("partial publication not retained: %+v %v", entry, err)
	}
	if sourceArchive(t, lib).Revision() != before.Revision() {
		t.Fatal("failed archive transaction changed original document")
	}
	_, err = Run(t.Context(), lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		t.Fatal("write ran over unresolved import publication")
		return nil
	})
	if progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("pending import did not block write: %v", err)
	}
	if _, err := Undo(t.Context(), lib, entry.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Original" {
		t.Fatalf("Engine recovery failed: %q %v", title, err)
	}
}
