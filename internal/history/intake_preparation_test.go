package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestIntakePreparationOwnsHistoryBeforeSnapshot(t *testing.T) {
	ctx := t.Context()
	lib, d, _ := fixture(t)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	prepared := false
	options := RunOptions{Operation: "track.edit", Scope: ScopeCollection, Prepare: func(ctx context.Context) error {
		other, err := lock(ctx, dir)
		if err == nil {
			other.Close()
			t.Fatal("preparation ran without exclusive History ownership")
		}
		prepared = true
		_, err = d.Exec("UPDATE Track SET title='Prepared'")
		return err
	}}
	entry, err := RunPrepared(ctx, lib, options, func(context.Context) error {
		if !prepared {
			t.Fatal("mutation ran before preparation")
		}
		_, err := d.Exec("UPDATE Track SET title='Changed'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Prepared" {
		t.Fatalf("Before snapshot did not follow preparation: %q %v", title, err)
	}
}

func TestIntakePreparationRefusesBeforeMutation(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepare failure", true: "interrupted operation"}[pending], func(t *testing.T) {
			ctx := t.Context()
			lib, _, _ := fixture(t)
			if pending {
				_, err := Run(ctx, lib, "interrupted", ScopeCollection, nil, RequireRecovery)
				if progress.ErrorCode(err) != "history_recovery_required" {
					t.Fatal(err)
				}
			}
			refusal := errors.New("backup refused")
			prepared, mutated := false, false
			options := RunOptions{Operation: "track.edit", Scope: ScopeCollection, Prepare: func(context.Context) error {
				prepared = true
				return refusal
			}}
			entry, err := RunPrepared(ctx, lib, options, func(context.Context) error { mutated = true; return nil })
			if entry != nil || err == nil || mutated || prepared == pending {
				t.Fatalf("refusal order: entry=%v err=%v prepared=%v mutated=%v", entry, err, prepared, mutated)
			}
			if !pending && !errors.Is(err, refusal) {
				t.Fatalf("preparation error lost: %v", err)
			}
			all, err := List(lib)
			if err != nil || len(all) != map[bool]int{false: 0, true: 1}[pending] {
				t.Fatalf("refusal created operation history: %v %v", all, err)
			}
		})
	}
}

func TestIntakeUndoAbsentTargetDoesNotEnrollHistory(t *testing.T) {
	for _, id := range []string{"", "missing"} {
		t.Run("target="+id, func(t *testing.T) {
			lib := t.TempDir()
			if entry, err := Undo(t.Context(), lib, id, UndoOptions{}); entry != nil || err == nil {
				t.Fatalf("absent target accepted: %v %v", entry, err)
			}
			if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("absent target enrolled History: %v", err)
			}
		})
	}
}

func TestIntakeUndoPreparationFailureRetainsTarget(t *testing.T) {
	ctx := t.Context()
	lib, d, _ := fixture(t)
	entry, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Changed'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("backup refused")
	called := false
	options := UndoOptions{Prepare: func(context.Context) error { called = true; return refusal }}
	if result, err := Undo(ctx, lib, entry.ID, options); result != nil || !errors.Is(err, refusal) || !called {
		t.Fatalf("Undo preparation not respected: %v %v called=%v", result, err, called)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Changed" {
		t.Fatalf("refused Undo changed collection: %q %v", title, err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatalf("refusal retained locks or consumed target: %v", err)
	}
}

func TestIntakeUndoSelectionRequiresHistoryOwnerBeforeReservation(t *testing.T) {
	ctx := t.Context()
	lib, d, _ := fixture(t)
	entry, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Changed'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := lock(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	owner, err := ownUndo(ctx, lib, dir, entry.ID)
	if owner != nil {
		owner.lease.Close()
		t.Fatal("Undo selected a target while another History writer was active")
	}
	if err == nil {
		t.Fatal("Undo ignored active History publication")
	}
	anchor := filepath.Join(filepath.Dir(lib), ".engine-cli-intake", filepath.Base(lib))
	if _, err := os.Lstat(anchor); !os.IsNotExist(err) {
		t.Fatalf("refused target selection enrolled replacement ownership: %v", err)
	}
}
