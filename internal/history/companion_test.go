package history

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func companionState(t *testing.T, lib string) tags.State {
	t.Helper()
	s, err := tags.Read(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompanionEnrollmentUndoRestoresAbsence(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	before := companionState(t, lib)
	entry, err := Run(ctx, lib, "tag.category.create", ScopeCollection, nil, func(ctx context.Context) error {
		_, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Mood", Revision: before.Revision()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(companionState(t, lib).Categories) != 1 {
		t.Fatal("category not committed")
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(companion.TagsPath(lib)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first-write undo retained companion: %v", err)
	}
}

func TestTrackRemovalPrunesCompanionWithinUndoBoundary(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	before := companionState(t, lib)
	state := tags.State{Categories: []tags.Category{{ID: "category", Name: "Mood", Values: []tags.Value{{ID: "value", Name: "Warm"}}}}, Assignments: []tags.Assignment{{OriginUUID: "origin", OriginTrackID: 7, ValueID: "value"}}}
	if _, err := tags.Replace(ctx, lib, before.Revision(), state); err != nil {
		t.Fatal(err)
	}
	entry, err := Run(ctx, lib, "track.remove", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("DELETE FROM Track WHERE id=1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	after := companionState(t, lib)
	if len(after.Assignments) != 0 || after.Categories[0].Values[0].TrackCount != 0 {
		t.Fatalf("dangling assignments: %+v", after)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	restored := companionState(t, lib)
	if len(restored.Assignments) != 1 || restored.Categories[0].Values[0].TrackCount != 1 {
		t.Fatalf("undo lost tags: %+v", restored)
	}
}

func TestExistingOrphansSurviveRenameUntilReconcileAndUndo(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := t.Context()
	state := tags.State{Categories: []tags.Category{{ID: "category", Name: "Mood", Values: []tags.Value{{ID: "value", Name: "Warm"}}}}, Assignments: []tags.Assignment{{OriginUUID: "origin", OriginTrackID: 7, ValueID: "value"}}}
	if _, err := tags.Replace(ctx, lib, companionState(t, lib).Revision(), state); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("DELETE FROM Track WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, lib, "tag.category.update", ScopeCollection, nil, func(ctx context.Context) error {
		_, err := tags.EditCategory(ctx, lib, tags.Edit{ID: "category", Name: "Atmosphere", Revision: companionState(t, lib).Revision()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := companionState(t, lib)
	if len(before.Assignments) != 1 {
		t.Fatal("unrelated rename silently repaired external removal")
	}
	preview, err := tags.Reconcile(ctx, lib, tags.ReconcileOptions{Revision: before.Revision(), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := Run(ctx, lib, "tag.reconcile", ScopeCollection, nil, func(ctx context.Context) error {
		_, err := tags.Reconcile(ctx, lib, tags.ReconcileOptions{Revision: preview.Revision, PlanToken: preview.PlanToken})
		return err
	})
	if err != nil || len(companionState(t, lib).Assignments) != 0 {
		t.Fatalf("reconcile entry=%+v error=%v", entry, err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := companionState(t, lib); got.Revision() != before.Revision() {
		t.Fatalf("undo failed to restore preserved orphan: %+v", got)
	}
}

func TestCrossFileFailureRetainsPendingRecovery(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	entry, err := Run(ctx, lib, "library.sync", ScopeCollection, nil, func(ctx context.Context) error {
		if _, err := d.Exec("UPDATE Track SET title='Published Engine half'"); err != nil {
			return err
		}
		return errors.Join(errors.New("companion publication failed"), RequireRecovery(ctx))
	})
	if progress.ErrorCode(err) != "history_recovery_required" || entry == nil || entry.After != nil {
		t.Fatalf("recovery not retained: %+v %v", entry, err)
	}
	_, err = Run(ctx, lib, "tag.category.create", ScopeCollection, nil, func(context.Context) error { t.Fatal("write ran over pending history"); return nil })
	if progress.ErrorCode(err) != "history_recovery_required" {
		t.Fatalf("pending entry did not block write: %v", err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Original" {
		t.Fatalf("recovery title=%q err=%v", title, err)
	}
	if _, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='After recovery'"); return err }); err != nil {
		t.Fatal(err)
	}
}
