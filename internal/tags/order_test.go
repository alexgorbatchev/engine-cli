package tags

import (
	"context"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// scale creates a category with the given values and returns their IDs and the latest revision.
func scale(t *testing.T, library, name string, values ...string) (string, []string, string) {
	t.Helper()
	ctx := t.Context()
	c, err := EditCategory(ctx, library, Edit{Name: name, Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	ids, rev := []string{}, c.Revision
	for _, value := range values {
		v, err := EditValue(ctx, library, Edit{Category: c.ID, Name: value, Revision: rev})
		if err != nil {
			t.Fatal(err)
		}
		ids, rev = append(ids, v.ID), v.Revision
	}
	return c.ID, ids, rev
}

func names(t *testing.T, library string) ([]string, []string) {
	t.Helper()
	s, err := Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	categories, values := []string{}, []string{}
	for _, c := range s.Categories {
		categories = append(categories, c.Name)
		for _, v := range c.Values {
			values = append(values, v.Name)
		}
	}
	return categories, values
}

func TestCategoriesAndValuesKeepTheirSavedOrder(t *testing.T) {
	ctx := t.Context()
	library, _ := tagLibrary(t)
	vibe, _, _ := scale(t, library, "Vibe")
	energy, values, rev := scale(t, library, "Energy", "Low", "Mid", "High")
	categories, listed := names(t, library)
	// New categories and values go last, whatever their identities sort as.
	if !slices.Equal(categories, []string{"Vibe", "Energy"}) || !slices.Equal(listed, []string{"Low", "Mid", "High"}) {
		t.Fatalf("creation order lost: %v %v", categories, listed)
	}
	moved, err := Move(ctx, library, MoveOptions{ID: energy, Before: vibe, Revision: rev}, true)
	if err != nil || !moved.Changed {
		t.Fatalf("category move = %+v, %v", moved, err)
	}
	moved, err = Move(ctx, library, MoveOptions{ID: values[2], Before: values[0], Revision: moved.Revision}, false)
	if err != nil || !moved.Changed {
		t.Fatalf("value move = %+v, %v", moved, err)
	}
	categories, listed = names(t, library)
	if !slices.Equal(categories, []string{"Energy", "Vibe"}) || !slices.Equal(listed, []string{"High", "Low", "Mid"}) {
		t.Fatalf("moves not saved: %v %v", categories, listed)
	}
	// Without an anchor a value moves to the end; moving it where it already is changes nothing.
	moved, err = Move(ctx, library, MoveOptions{ID: values[2], Revision: moved.Revision}, false)
	if err != nil || !moved.Changed {
		t.Fatalf("move to end = %+v, %v", moved, err)
	}
	unchanged, err := Move(ctx, library, MoveOptions{ID: values[2], Revision: moved.Revision}, false)
	if err != nil || unchanged.Changed || unchanged.Revision != moved.Revision {
		t.Fatalf("repeated move = %+v, %v", unchanged, err)
	}
	if _, listed = names(t, library); !slices.Equal(listed, []string{"Low", "Mid", "High"}) {
		t.Fatalf("move to end not saved: %v", listed)
	}
	requireCode(t, CheckMove(ctx, library, MoveOptions{ID: values[0], Before: vibe, Revision: moved.Revision}, false), "tag_move_invalid")
	requireCode(t, CheckMove(ctx, library, MoveOptions{ID: values[0], Before: values[0], Revision: moved.Revision}, false), "tag_move_invalid")
	requireCode(t, CheckMove(ctx, library, MoveOptions{ID: "missing", Revision: moved.Revision}, true), "tag_not_found")
	requireCode(t, CheckMove(ctx, library, MoveOptions{ID: vibe, Revision: "stale"}, true), "tag_revision_stale")
	if err := CheckMove(ctx, library, MoveOptions{ID: vibe, Before: energy, Revision: moved.Revision}, true); err != nil {
		t.Fatal(err)
	}
	_, err = Move(ctx, library, MoveOptions{ID: "missing", Revision: moved.Revision}, false)
	requireCode(t, err, "tag_not_found")
	_, err = Move(ctx, library, MoveOptions{ID: values[0], Before: vibe, Revision: moved.Revision}, false)
	requireCode(t, err, "tag_move_invalid")
}

func TestVersionOneStoresReadInIdentityOrderAndUpgradeOnWrite(t *testing.T) {
	ctx := t.Context()
	library, _ := tagLibrary(t)
	_, _, _ = scale(t, library, "Vibe", "Dark")
	d, err := db.Open(companion.TagsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the version 1 layout an older Engine CLI wrote.
	_, err = d.Exec(`CREATE TABLE OldCategory (id TEXT NOT NULL PRIMARY KEY, name TEXT NOT NULL, nameKey TEXT NOT NULL UNIQUE);
INSERT INTO OldCategory VALUES('b','Zeta','zeta'),('a','Alpha','alpha');
DELETE FROM Assignment; DELETE FROM Value; DELETE FROM Category;
ALTER TABLE Category DROP COLUMN position; ALTER TABLE Category DROP COLUMN mode; ALTER TABLE Value DROP COLUMN position;
INSERT INTO Category SELECT * FROM OldCategory; DROP TABLE OldCategory; UPDATE Metadata SET version=1;`)
	if closeErr := d.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	categories, _ := names(t, library)
	if !slices.Equal(categories, []string{"Alpha", "Zeta"}) {
		t.Fatalf("version 1 order = %v", categories)
	}
	s, err := Read(ctx, library)
	if err != nil || s.Categories[0].Mode != ModeMultiple {
		t.Fatalf("version 1 mode = %+v, %v", s.Categories, err)
	}
	if err := ValidateDatabase(ctx, companion.TagsPath(library)); err != nil {
		t.Fatal(err)
	}
	created, err := EditCategory(ctx, library, Edit{Name: "Energy", Revision: s.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	if categories, _ = names(t, library); !slices.Equal(categories, []string{"Alpha", "Zeta", "Energy"}) {
		t.Fatalf("upgrade order = %v", categories)
	}
	if _, err := SetMode(ctx, library, ModeOptions{ID: created.ID, Mode: ModeSingle, Revision: created.Revision}); err != nil {
		t.Fatal(err)
	}
}

func TestSingleChoiceCategoriesHoldOneValuePerTrack(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	energy, values, rev := scale(t, library, "Energy", "Low", "Mid", "High")
	low, mid, high := values[0], values[1], values[2]
	// Two values on one track keep a category multiple-choice until one comes off.
	assigned, err := Assign(ctx, engine, library, AssignmentEdit{Track: "1", Values: []string{low, mid}, Revision: rev})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, CheckMode(ctx, library, ModeOptions{ID: energy, Mode: ModeSingle, Revision: assigned.Revision}), "tag_mode_conflict")
	_, err = SetMode(ctx, library, ModeOptions{ID: energy, Mode: ModeSingle, Revision: assigned.Revision})
	requireCode(t, err, "tag_mode_conflict")
	requireCode(t, CheckMode(ctx, library, ModeOptions{ID: energy, Mode: "scale", Revision: assigned.Revision}), "tag_mode_invalid")
	requireCode(t, CheckMode(ctx, library, ModeOptions{ID: "missing", Mode: ModeSingle, Revision: assigned.Revision}), "tag_not_found")
	requireCode(t, CheckMode(ctx, library, ModeOptions{ID: energy, Mode: ModeSingle, Revision: "stale"}), "tag_revision_stale")
	assigned, err = Assign(ctx, engine, library, AssignmentEdit{Track: "1", Values: []string{low}, Revision: assigned.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckMode(ctx, library, ModeOptions{ID: energy, Mode: ModeSingle, Revision: assigned.Revision}); err != nil {
		t.Fatal(err)
	}
	single, err := SetMode(ctx, library, ModeOptions{ID: energy, Mode: ModeSingle, Revision: assigned.Revision})
	if err != nil || !single.Changed {
		t.Fatalf("mode = %+v, %v", single, err)
	}
	again, err := SetMode(ctx, library, ModeOptions{ID: energy, Mode: ModeSingle, Revision: single.Revision})
	if err != nil || again.Changed {
		t.Fatalf("repeated mode = %+v, %v", again, err)
	}
	// Assigning two values of a single-choice category is refused.
	requireCode(t, CheckAssignment(ctx, engine, library, AssignmentEdit{Track: "1", Values: []string{low, high}, Revision: single.Revision}), "tag_single_conflict")
	_, err = Assign(ctx, engine, library, AssignmentEdit{Track: "1", Values: []string{low, high}, Revision: single.Revision})
	requireCode(t, err, "tag_single_conflict")
	options := BatchOptions{Tracks: []string{"1", "2"}, Revision: single.Revision, Add: []string{low, high}, Remove: []string{}, DryRun: true}
	_, err = AssignBatch(ctx, engine, library, options)
	requireCode(t, err, "tag_single_conflict")
	// Choosing High replaces Low in the same write and undo record.
	options.Add = []string{high}
	preview, err := AssignBatch(ctx, engine, library, options)
	if err != nil || !slices.Equal(preview.Replaced, sorted(low, mid)) {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if preview.ChangedTracks != 2 || preview.AddedAssignments != 2 || preview.RemovedAssignments != 1 {
		t.Fatalf("preview counts = %+v", preview)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	if _, err := AssignBatch(ctx, engine, library, options); err != nil {
		t.Fatal(err)
	}
	tags, err := InspectTrack(ctx, engine, library, "1")
	if err != nil || !slices.Equal(tags.Values, []string{high}) {
		t.Fatalf("track 1 = %+v, %v", tags, err)
	}
}

func sorted(values ...string) []string {
	slices.Sort(values)
	return values
}

func TestReplacementKeepsOrderAndMode(t *testing.T) {
	ctx := context.Background()
	library, _ := tagLibrary(t)
	next := State{
		Categories: []Category{
			{ID: "c2", Name: "Energy", Mode: ModeSingle, Values: []Value{{ID: "v2", Name: "High"}, {ID: "v1", Name: "Low"}}},
			{ID: "c1", Name: "Vibe", Values: []Value{}},
		},
		Assignments: []Assignment{},
	}
	if _, err := Replace(ctx, library, currentRevision(t, library), next); err != nil {
		t.Fatal(err)
	}
	s, err := Read(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if s.Categories[0].ID != "c2" || s.Categories[0].Mode != ModeSingle || s.Categories[1].Mode != ModeMultiple || s.Categories[0].Values[0].ID != "v2" {
		t.Fatalf("replacement = %+v", s.Categories)
	}
}
