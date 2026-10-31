package duplicate

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestMergeColorInheritanceAndUndo(t *testing.T) {
	library := dbtest.Library(t, "local")
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)
	empty, err := colors.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	before, err := colors.Replace(t.Context(), library, empty.Revision(), colors.State{Assignments: []colors.Assignment{
		{OriginUUID: "second", OriginTrackID: 202, Color: "#CC3300"},
		{OriginUUID: "older-unavailable", OriginTrackID: 99, Color: "#112233"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Inspect(t.Context(), d, 2)
	if err != nil || selected.Color != "#CC3300" {
		t.Fatalf("review color=%+v error=%v", selected, err)
	}
	entry, err := history.Run(t.Context(), library, "duplicate.merge", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := Merge(ctx, d, MergeOptions{Keep: 1, Remove: []int64{2}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := colors.InspectTrack(t.Context(), d, library, "1")
	if err != nil || after.Color != "#CC3300" {
		t.Fatalf("survivor=%+v error=%v", after, err)
	}
	state, err := colors.Read(t.Context(), library)
	if err != nil || len(state.Assignments) != 2 {
		t.Fatalf("merged state=%+v error=%v", state, err)
	}
	if _, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	state, err = colors.Read(t.Context(), library)
	if err != nil || state.Revision() != before.Revision() {
		t.Fatalf("undo state=%+v error=%v", state, err)
	}
}
