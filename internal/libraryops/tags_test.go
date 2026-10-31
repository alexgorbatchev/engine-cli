package libraryops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func assignedLibrary(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	lib := fixture(t, "")
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var origin string
	var id int64
	if err := d.QueryRow("SELECT originDatabaseUuid,originTrackId FROM Track WHERE id=1").Scan(&origin, &id); err != nil {
		t.Fatal(err)
	}
	empty, err := tags.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	state := tags.State{Categories: []tags.Category{{ID: "category", Name: "Mood", Values: []tags.Value{{ID: "value", Name: "Warm"}}}}, Assignments: []tags.Assignment{{OriginUUID: origin, OriginTrackID: id, ValueID: "value"}}}
	if _, err := tags.Replace(ctx, lib, empty.Revision(), state); err != nil {
		t.Fatal(err)
	}
	stored, err := tags.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	colorState, err := colors.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := colors.Assign(ctx, d, lib, colors.Edit{Track: "1", Color: "#123456", Revision: colorState.Revision()}); err != nil {
		t.Fatal(err)
	}
	return lib, stored.Revision()
}

func TestLibraryMoveDeletePreserveCompanionUndo(t *testing.T) {
	for _, action := range []string{"move", "delete"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			lib, revision := assignedLibrary(t)
			opts := Options{Path: lib, Action: action, ConfirmPath: lib}
			if action == "move" {
				opts.Destination = filepath.Join(t.TempDir(), "moved")
			}
			plan, err := Preview(ctx, opts, nil)
			if err != nil || !plan.Applicable {
				t.Fatalf("plan: %+v %v", plan, err)
			}
			opts.PlanToken = plan.PlanToken
			entry, err := history.Run(ctx, lib, "library."+action, history.ScopeCollection, nil, func(ctx context.Context) error { return Apply(ctx, opts, nil) })
			if err != nil {
				t.Fatal(err)
			}
			undoPath := lib
			if action == "move" {
				undoPath = opts.Destination
				state, err := tags.Read(ctx, undoPath)
				if err != nil || state.Revision() != revision {
					t.Fatalf("move lost companion: %+v %v", state, err)
				}
				moved, err := colors.Read(ctx, undoPath)
				if err != nil || len(moved.Assignments) != 1 || moved.Assignments[0].Color != "#123456" {
					t.Fatalf("move lost colors: %+v %v", moved, err)
				}
			}
			if _, err := os.Stat(companion.TagsPath(lib)); !os.IsNotExist(err) {
				t.Fatalf("source companion remained: %v", err)
			}
			if _, err := os.Stat(companion.ColorsPath(lib)); !os.IsNotExist(err) {
				t.Fatalf("source colors remained: %v", err)
			}
			if _, err := history.Undo(ctx, undoPath, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			state, err := tags.Read(ctx, lib)
			if err != nil || state.Revision() != revision || state.Categories[0].Values[0].TrackCount != 1 {
				t.Fatalf("undo lost companion: %+v %v", state, err)
			}
			restored, err := colors.Read(ctx, lib)
			if err != nil || len(restored.Assignments) != 1 || restored.Assignments[0].Color != "#123456" {
				t.Fatalf("undo lost colors: %+v %v", restored, err)
			}
		})
	}
}

func TestDeletePlanRejectsChangedColor(t *testing.T) {
	library, _ := assignedLibrary(t)
	options := Options{Path: library, Action: "delete", ConfirmPath: library}
	plan, err := Preview(t.Context(), options, nil)
	if err != nil {
		t.Fatal(err)
	}
	options.PlanToken = plan.PlanToken
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	before, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := colors.Assign(t.Context(), d, library, colors.Edit{Track: "1", Color: "#ABCDEF", Revision: before.Revision()})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(t.Context(), options, nil); err == nil {
		t.Fatal("deleted library after color changed")
	}
	stored, err := colors.Read(t.Context(), library)
	if err != nil || stored.Revision() != changed.Revision {
		t.Fatalf("stale delete touched colors: %+v %v", stored, err)
	}
	if _, err := os.Stat(filepath.Join(library, "Database2", "m.db")); err != nil {
		t.Fatal("stale delete removed Engine database", err)
	}
}

func TestDeletePlanRejectsChangedCompanion(t *testing.T) {
	ctx := context.Background()
	lib, revision := assignedLibrary(t)
	opts := Options{Path: lib, Action: "delete", ConfirmPath: lib}
	plan, err := Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := tags.EditCategory(ctx, lib, tags.Edit{ID: "category", Name: "Energy", Revision: revision}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, opts, nil); err == nil {
		t.Fatal("deleted changed companion with stale plan")
	}
	state, err := tags.List(ctx, lib)
	if err != nil || state.Categories[0].Name != "Energy" {
		t.Fatalf("stale plan touched companion: %+v %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Database2", "m.db")); err != nil {
		t.Fatal("stale plan deleted Engine database", err)
	}
}
