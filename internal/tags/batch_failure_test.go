package tags

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestBatchRejectsUnreadableCompanion(t *testing.T) {
	library, engine := tagLibrary(t)
	path := companion.TagsPath(library)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := InspectBatch(t.Context(), engine, library, []string{"1"})
	requireCode(t, err, "tag_store_unavailable")
	_, err = AssignBatch(t.Context(), engine, library, BatchOptions{Tracks: []string{"1"}, Add: []string{}, Remove: []string{}, DryRun: true})
	requireCode(t, err, "tag_store_unavailable")
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "not SQLite" {
		t.Fatalf("failed batch modified unreadable companion: %q error=%v", contents, err)
	}
}

func TestBatchDeleteFailureRollsBackEarlierRemovals(t *testing.T) {
	ctx := t.Context()
	library, engine := tagLibrary(t)
	c, err := EditCategory(ctx, library, Edit{Name: "Energy", Revision: currentRevision(t, library)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := EditValue(ctx, library, Edit{Category: c.ID, Name: "Warm", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	options := BatchOptions{Tracks: []string{"1", "2"}, Revision: v.Revision, Add: []string{v.ID}, Remove: []string{}, DryRun: true}
	preview, err := AssignBatch(ctx, engine, library, options)
	if err != nil {
		t.Fatal(err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	applied, err := AssignBatch(ctx, engine, library, options)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(companion.TagsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TRIGGER reject_second_removal BEFORE DELETE ON Assignment WHEN OLD.originTrackId=20 BEGIN SELECT RAISE(ABORT,'injected removal failure'); END`); err != nil {
		t.Fatal(err)
	}
	options.Revision, options.DryRun = applied.Revision, true
	options.Add, options.Remove = []string{}, []string{v.ID}
	preview, err = AssignBatch(ctx, engine, library, options)
	if err != nil || preview.RemovedAssignments != 2 {
		t.Fatalf("removal preview=%+v error=%v", preview, err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	if _, err := AssignBatch(ctx, engine, library, options); err == nil {
		t.Fatal("failing second removal reported success")
	}
	state, err := Read(ctx, library)
	if err != nil || len(state.Assignments) != 2 || state.Revision() != applied.Revision {
		t.Fatalf("failed removal retained a partial write: %+v error=%v", state, err)
	}
}
