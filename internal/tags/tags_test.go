package tags

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func tagLibrary(t *testing.T) (string, *sql.DB) {
	t.Helper()
	library := dbtest.Library(t, "library-a")
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec(`INSERT INTO Track(id,path,title,artist,originDatabaseUuid,originTrackId) VALUES(1,'one.mp3','One','Artist','library-a',10),(2,'two.mp3','Two','Artist','library-a',20)`); err != nil {
		t.Fatal(err)
	}
	return library, d
}

func currentRevision(t *testing.T, library string) string {
	t.Helper()
	s, err := Read(context.Background(), library)
	if err != nil {
		t.Fatal(err)
	}
	return s.Revision()
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || progress.ErrorCode(err) != code {
		t.Fatalf("error=%v code=%s; want %s", err, progress.ErrorCode(err), code)
	}
}

func TestTaxonomyAssignmentsAndExactDeletion(t *testing.T) {
	ctx := context.Background()
	library, d := tagLibrary(t)
	initial := currentRevision(t, library)
	category, err := EditCategory(ctx, library, Edit{Name: "  Café  ", Revision: initial})
	if err != nil {
		t.Fatal(err)
	}
	_, err = EditCategory(ctx, library, Edit{Name: "CAFE\u0301", Revision: category.Revision})
	requireCode(t, err, "tag_name_exists")
	first, err := EditValue(ctx, library, Edit{Category: category.ID, Name: "Warm", Revision: category.Revision})
	if err != nil {
		t.Fatal(err)
	}
	second, err := EditValue(ctx, library, Edit{Category: category.ID, Name: "Driving", Revision: first.Revision})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := Assign(ctx, d, library, AssignmentEdit{Track: "1", Values: []string{first.ID, second.ID}, Revision: second.Revision})
	if err != nil || len(assigned.Values) != 2 {
		t.Fatalf("assign=%+v err=%v", assigned, err)
	}
	_, err = Assign(ctx, d, library, AssignmentEdit{Track: "2", Values: []string{first.ID}, Revision: second.Revision})
	requireCode(t, err, "tag_revision_stale")
	listed, err := List(ctx, library)
	if err != nil || listed.Categories[0].Name != "Café" {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	for _, value := range listed.Categories[0].Values {
		if value.TrackCount != 1 {
			t.Fatalf("count=%+v", value)
		}
	}
	preview, err := Delete(ctx, library, DeleteOptions{ID: category.ID, Revision: assigned.Revision, DryRun: true}, true)
	if err != nil || preview.AffectedTracks != 1 || preview.RemovedValues != 2 || preview.RemovedAssignments != 2 || preview.Applied {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	_, err = Delete(ctx, library, DeleteOptions{ID: first.ID, Revision: assigned.Revision, PlanToken: preview.PlanToken}, false)
	requireCode(t, err, "tag_plan_stale")
	deleted, err := Delete(ctx, library, DeleteOptions{ID: category.ID, Revision: assigned.Revision, PlanToken: preview.PlanToken}, true)
	if err != nil || !deleted.Applied || deleted.Revision == assigned.Revision {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	track, err := InspectTrack(ctx, d, library, "1")
	if err != nil || len(track.Values) != 0 {
		t.Fatalf("track=%+v err=%v", track, err)
	}
}

func TestRejectedFirstMutationPreservesAbsence(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(context.Context, string, *sql.DB, string) error
	}{
		{"unknown category", func(ctx context.Context, lib string, _ *sql.DB, rev string) error {
			_, err := EditValue(ctx, lib, Edit{Category: "missing", Name: "Warm", Revision: rev})
			return err
		}},
		{"unknown value", func(ctx context.Context, lib string, d *sql.DB, rev string) error {
			_, err := Assign(ctx, d, lib, AssignmentEdit{Track: "1", Values: []string{"missing"}, Revision: rev})
			return err
		}},
		{"unknown track", func(ctx context.Context, lib string, d *sql.DB, rev string) error {
			_, err := Assign(ctx, d, lib, AssignmentEdit{Track: "999", Values: []string{}, Revision: rev})
			return err
		}},
		{"commit failure", func(ctx context.Context, lib string, _ *sql.DB, rev string) error {
			_, err := mutate(ctx, lib, rev, func(tx *sql.Tx, _ State) (string, bool, error) {
				_, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON; INSERT INTO Assignment VALUES('library-a',10,'missing')`)
				return "", true, err
			})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lib, d := tagLibrary(t)
			rev := currentRevision(t, lib)
			if err := test.run(context.Background(), lib, d, rev); err == nil {
				t.Fatal("rejected operation succeeded")
			}
			if _, err := os.Stat(companion.TagsPath(lib)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed mutation enrolled a store: %v", err)
			}
			if got := currentRevision(t, lib); got != rev {
				t.Fatalf("empty revision changed: %s", got)
			}
		})
	}
}

func TestNameValidation(t *testing.T) {
	for _, name := range []string{"", "  ", "bad\nname", strings.Repeat("a", 101), string([]byte{0xff})} {
		_, _, err := normalizedName(name)
		requireCode(t, err, "tag_name_invalid")
	}
}

func TestRejectedReplacementRollsBackStoredTaxonomy(t *testing.T) {
	for _, test := range []struct {
		name string
		next State
	}{
		{"invalid category", State{Categories: []Category{{ID: "replacement", Name: "bad\nname"}}}},
		{"duplicate category", State{Categories: []Category{{ID: "one", Name: "Mood"}, {ID: "two", Name: "MOOD"}}}},
		{"invalid value", State{Categories: []Category{{ID: "replacement", Name: "Energy", Values: []Value{{ID: "value", Name: ""}}}}}},
		{"duplicate value", State{Categories: []Category{{ID: "replacement", Name: "Energy", Values: []Value{{ID: "one", Name: "Warm"}, {ID: "two", Name: "WARM"}}}}}},
		{"unknown assignment", State{Categories: []Category{}, Assignments: []Assignment{{OriginUUID: "library-a", OriginTrackID: 10, ValueID: "missing"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lib, d := tagLibrary(t)
			category, err := EditCategory(t.Context(), lib, Edit{Name: "Original", Revision: currentRevision(t, lib)})
			if err != nil {
				t.Fatal(err)
			}
			value, err := EditValue(t.Context(), lib, Edit{Category: category.ID, Name: "Retained", Revision: category.Revision})
			if err != nil {
				t.Fatal(err)
			}
			assigned, err := Assign(t.Context(), d, lib, AssignmentEdit{Track: "1", Values: []string{value.ID}, Revision: value.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Replace(t.Context(), lib, assigned.Revision, test.next); err == nil {
				t.Fatal("invalid replacement committed")
			}
			if got := currentRevision(t, lib); got != assigned.Revision {
				t.Fatalf("failed replacement changed definitions or assignments: got %s want %s", got, assigned.Revision)
			}
		})
	}
}

func TestAssignmentReplacementAndValidation(t *testing.T) {
	ctx := context.Background()
	lib, d := tagLibrary(t)
	c, err := EditCategory(ctx, lib, Edit{Name: "Mood", Revision: currentRevision(t, lib)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := EditValue(ctx, lib, Edit{Name: "Warm", Category: c.ID, Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		values []string
		code   string
	}{{nil, "tag_values_required"}, {[]string{v.ID, v.ID}, "tag_value_duplicate"}, {[]string{"foreign"}, "tag_not_found"}} {
		_, err := Assign(ctx, d, lib, AssignmentEdit{Track: "1", Values: test.values, Revision: v.Revision})
		requireCode(t, err, test.code)
	}
	assigned, err := Assign(ctx, d, lib, AssignmentEdit{Track: "1", Values: []string{v.ID}, Revision: v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := Assign(ctx, d, lib, AssignmentEdit{Track: "1", Values: []string{}, Revision: assigned.Revision})
	if err != nil || cleared.Values == nil || len(cleared.Values) != 0 {
		t.Fatalf("clear=%+v %v", cleared, err)
	}
	state, err := List(ctx, lib)
	if err != nil || state.Categories[0].Values[0].TrackCount != 0 {
		t.Fatalf("state=%+v %v", state, err)
	}
}
