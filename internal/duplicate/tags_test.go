package duplicate

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestMergeCustomTagsUnionsSurvivorAndUndoRestoresOrigins(t *testing.T) {
	ctx := context.Background()
	lib := dbtest.Library(t, "local")
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seed(t, d)
	empty, err := tags.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	want := tags.State{
		Categories:  []tags.Category{{ID: "category", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}, {ID: "deep", Name: "Deep"}}}},
		Assignments: []tags.Assignment{{OriginUUID: "first", OriginTrackID: 101, ValueID: "warm"}, {OriginUUID: "second", OriginTrackID: 202, ValueID: "warm"}, {OriginUUID: "second", OriginTrackID: 202, ValueID: "deep"}},
	}
	if _, err := tags.Replace(ctx, lib, empty.Revision(), want); err != nil {
		t.Fatal(err)
	}
	before, err := tags.Read(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(ctx, lib, "duplicate.merge", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := Merge(ctx, d, MergeOptions{Keep: 1, Remove: []int64{2}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := tags.InspectTrack(ctx, d, lib, "1")
	if err != nil || !reflect.DeepEqual(after.Values, []string{"deep", "warm"}) {
		t.Fatalf("survivor tags: %+v %v", after, err)
	}
	state, err := tags.Read(ctx, lib)
	if err != nil || len(state.Assignments) != 2 {
		t.Fatalf("merged assignments: %+v %v", state, err)
	}
	for _, value := range state.Categories[0].Values {
		if value.TrackCount != 1 {
			t.Fatalf("duplicate inflated impact: %+v", value)
		}
	}
	if _, err := history.Undo(ctx, lib, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := tags.Read(ctx, lib)
	if err != nil || restored.Revision() != before.Revision() {
		t.Fatalf("undo lost origin assignments: %+v %v", restored, err)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track WHERE id IN (1,2)").Scan(&count); err != nil || count != 2 {
		t.Fatalf("undo tracks=%d %v", count, err)
	}
}
