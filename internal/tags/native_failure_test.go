package tags

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func editTagStorage(t *testing.T, library, statement string) {
	t.Helper()
	d, err := db.Open(companion.TagsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(statement); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func tagBytes(t *testing.T, library string) []byte {
	t.Helper()
	data, err := os.ReadFile(companion.TagsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func taggedTrack(t *testing.T) (string, *sql.DB, string, string) {
	t.Helper()
	library, engine := tagLibrary(t)
	c, err := EditCategory(t.Context(), library, Edit{Name: "Mood", Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := EditValue(t.Context(), library, Edit{Category: c.ID, Name: "Warm", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Assign(t.Context(), engine, library, AssignmentEdit{Track: "1", Values: []string{v.ID}, Revision: v.Revision}); err != nil {
		t.Fatal(err)
	}
	return library, engine, c.ID, v.ID
}

func TestDamagedTagTablesAreNeverRepairedByRead(t *testing.T) {
	for _, table := range []string{"Metadata", "Category", "Value", "Assignment"} {
		t.Run(table, func(t *testing.T) {
			library, _, _, _ := taggedTrack(t)
			editTagStorage(t, library, "PRAGMA foreign_keys=OFF; DROP TABLE "+table)
			before := tagBytes(t, library)
			_, err := Read(t.Context(), library)
			requireCode(t, err, "tag_store_unavailable")
			if !bytes.Equal(before, tagBytes(t, library)) {
				t.Fatal("read modified damaged storage")
			}
		})
	}
}

func TestTagOperationsPreserveUnreadableCompanion(t *testing.T) {
	operations := map[string]func(context.Context, *sql.DB, string) error{
		"inspect": func(ctx context.Context, d *sql.DB, lib string) error {
			_, err := InspectTrack(ctx, d, lib, "1")
			return err
		},
		"edit": func(ctx context.Context, _ *sql.DB, lib string) error {
			_, err := EditCategory(ctx, lib, Edit{Name: "New"})
			return err
		},
		"check edit": func(ctx context.Context, _ *sql.DB, lib string) error {
			return CheckEdit(ctx, lib, Edit{Name: "New"}, true)
		},
		"check assignment": func(ctx context.Context, d *sql.DB, lib string) error {
			return CheckAssignment(ctx, d, lib, AssignmentEdit{Track: "1", Values: []string{}})
		},
		"delete": func(ctx context.Context, _ *sql.DB, lib string) error {
			_, err := Delete(ctx, lib, DeleteOptions{}, true)
			return err
		},
		"check deletion": func(ctx context.Context, _ *sql.DB, lib string) error {
			return CheckDelete(ctx, lib, DeleteOptions{}, true)
		},
		"reconcile": func(ctx context.Context, _ *sql.DB, lib string) error {
			_, err := Reconcile(ctx, lib, ReconcileOptions{})
			return err
		},
		"check reconciliation": func(ctx context.Context, _ *sql.DB, lib string) error {
			return CheckReconciliation(ctx, lib, ReconcileOptions{})
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			library, engine := tagLibrary(t)
			if err := os.MkdirAll(filepath.Dir(companion.TagsPath(library)), 0700); err != nil {
				t.Fatal(err)
			}
			const damaged = "unreadable companion bytes"
			if err := os.WriteFile(companion.TagsPath(library), []byte(damaged), 0600); err != nil {
				t.Fatal(err)
			}
			requireCode(t, operation(t.Context(), engine, library), "tag_store_unavailable")
			if string(tagBytes(t, library)) != damaged {
				t.Fatal("failed operation modified unreadable companion")
			}
		})
	}
}

func TestAssignmentFailureRollsBackPreviousValues(t *testing.T) {
	for _, event := range []string{"DELETE", "INSERT"} {
		t.Run(event, func(t *testing.T) {
			library, engine, category, previous := taggedTrack(t)
			value, err := EditValue(t.Context(), library, Edit{Category: category, Name: "Driving", Revision: currentRevision(t, library)})
			if err != nil {
				t.Fatal(err)
			}
			editTagStorage(t, library, "CREATE TRIGGER reject_assignment BEFORE "+event+" ON Assignment BEGIN SELECT RAISE(ABORT,'native write rejected'); END")
			_, err = Assign(t.Context(), engine, library, AssignmentEdit{Track: "1", Values: []string{value.ID}, Revision: value.Revision})
			requireCode(t, err, "tag_store_unavailable")
			track, err := InspectTrack(t.Context(), engine, library, "1")
			if err != nil || track.Revision != value.Revision || len(track.Values) != 1 || track.Values[0] != previous {
				t.Fatalf("failed assignment changed previous values: %+v %v", track, err)
			}
		})
	}
}

func TestSnapshotValidationRejectsOrphanAssignment(t *testing.T) {
	library, _, _, _ := taggedTrack(t)
	editTagStorage(t, library, "PRAGMA foreign_keys=OFF; INSERT INTO Assignment VALUES('library-a',10,'missing-value')")
	before := tagBytes(t, library)
	requireCode(t, ValidateDatabase(t.Context(), companion.TagsPath(library)), "tag_store_unavailable")
	if !bytes.Equal(before, tagBytes(t, library)) {
		t.Fatal("snapshot validation modified invalid references")
	}
}

func TestMissingEngineOriginsCannotAuthorizeTagRemoval(t *testing.T) {
	for _, name := range []string{"list", "delete", "check deletion", "reconcile", "check reconciliation", "removed before", "removed current"} {
		t.Run(name, func(t *testing.T) {
			library, engine, category, _ := taggedTrack(t)
			rev := currentRevision(t, library)
			before := tagBytes(t, library)
			if name != "removed before" {
				if _, err := engine.Exec("ALTER TABLE Track RENAME TO UnavailableTrack"); err != nil {
					t.Fatal(err)
				}
			}
			options := DeleteOptions{ID: category, Revision: rev, DryRun: true}
			reconcile := ReconcileOptions{Revision: rev, DryRun: true}
			var err error
			switch name {
			case "list":
				_, err = List(t.Context(), library)
			case "delete":
				_, err = Delete(t.Context(), library, options, true)
			case "check deletion":
				err = CheckDelete(t.Context(), library, options, true)
			case "reconcile":
				_, err = Reconcile(t.Context(), library, reconcile)
			case "check reconciliation":
				err = CheckReconciliation(t.Context(), library, reconcile)
			case "removed before":
				err = ReconcileRemoved(t.Context(), library, filepath.Join(t.TempDir(), "missing.db"))
			case "removed current":
				previous, _ := tagLibrary(t)
				err = ReconcileRemoved(t.Context(), library, filepath.Join(previous, "Database2", "m.db"))
			}
			if err == nil {
				t.Fatal("unreadable Engine origins were treated as a valid empty collection")
			}
			if !bytes.Equal(before, tagBytes(t, library)) {
				t.Fatal("failed origin read modified companion assignments")
			}
		})
	}
}

func TestInvalidTrackIdentityNeverEnrollsTagStorage(t *testing.T) {
	for _, identity := range []string{"negative ID", "unreadable Track table"} {
		t.Run(identity, func(t *testing.T) {
			library, engine := tagLibrary(t)
			statement := "UPDATE Track SET originTrackId=-1 WHERE id=1"
			if identity == "unreadable Track table" {
				statement = "ALTER TABLE Track RENAME TO UnavailableTrack"
			}
			if _, err := engine.Exec(statement); err != nil {
				t.Fatal(err)
			}
			_, err := InspectTrack(t.Context(), engine, library, "1")
			if identity == "unreadable Track table" {
				if err == nil {
					t.Fatal("inspection accepted an unreadable collection")
				}
			} else {
				requireCode(t, err, "tag_track_identity_invalid")
			}
			if _, err := os.Stat(companion.TagsPath(library)); !os.IsNotExist(err) {
				t.Fatalf("failed identity check enrolled companion: %v", err)
			}
		})
	}
}

func TestOrphanValueReadPreservesDamagedStorage(t *testing.T) {
	library, _, category, _ := taggedTrack(t)
	editTagStorage(t, library, "PRAGMA foreign_keys=OFF; DELETE FROM Category WHERE id='"+category+"'")
	before := tagBytes(t, library)
	_, err := Read(t.Context(), library)
	requireCode(t, err, "tag_store_unavailable")
	if !bytes.Equal(before, tagBytes(t, library)) {
		t.Fatal("read repaired orphaned values without consent")
	}
}
