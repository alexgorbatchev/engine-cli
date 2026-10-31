package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestUndoReplacementOwnershipAndTargetGeneration(t *testing.T) {
	ctx := context.Background()
	lib, d, _ := fixture(t)
	first, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='First'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ownUndo(ctx, lib, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.lease.Close()
	if lease, err := intakelease.Observe(ctx, lib); lease != nil || progress.ErrorCode(err) != "intake_replacing" {
		t.Fatalf("undo did not exclude queue owner: %v %v", lease, err)
	}
	if _, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Later'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	all, err := entries(dir)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := selectUndoTarget(all, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.validate(latest); err == nil {
		t.Fatal("latest Undo silently changed after reservation selection")
	}
	explicit, err := selectUndoTarget(all, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.validate(explicit); err != nil {
		t.Fatalf("unchanged explicit operation refused: %v", err)
	}
}

func TestUndoRefusesForeignQueueAndInvalidTargetBeforeReservation(t *testing.T) {
	ctx := context.Background()
	lib, d, _ := fixture(t)
	entry, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Changed'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(filepath.Dir(lib), ".engine-cli-intake", filepath.Base(lib))
	if _, err := Undo(ctx, lib, "missing", UndoOptions{}); err == nil {
		t.Fatal("missing target accepted")
	}
	if _, err := os.Stat(anchor); !os.IsNotExist(err) {
		t.Fatalf("invalid target enrolled replacement owner: %v", err)
	}
	observer, err := intakelease.Observe(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := Undo(ctx, lib, entry.ID, UndoOptions{}); report != nil || progress.ErrorCode(err) != "intake_replacing" {
		t.Fatalf("Undo replaced a live queue: %v %v", report, err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Changed" {
		t.Fatalf("refused Undo mutated collection: %q %v", title, err)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Original" {
		t.Fatalf("released Undo did not restore collection: %q %v", title, err)
	}
}

func TestUndoRelocationRetainsBothPathsThroughPublication(t *testing.T) {
	ctx := t.Context()
	source, d, media := fixture(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "moved")
	entry, err := Run(ctx, source, "library.move", ScopeCollection, nil, func(ctx context.Context) error {
		return RelocateLibrary(ctx, source, destination)
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, proceed := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	released, joined := false, false
	go func() {
		_, err := Undo(ctx, destination, entry.ID, UndoOptions{Prepare: func(ctx context.Context) error {
			close(ready)
			select {
			case <-proceed:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
		done <- err
	}()
	defer func() {
		if !released {
			close(proceed)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		joined = true
		t.Fatalf("Undo did not reach preparation: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, path := range []string{source, destination} {
		lease, err := intakelease.Observe(ctx, path)
		if lease != nil {
			lease.Close()
		}
		if lease != nil || progress.ErrorCode(err) != "intake_replacing" {
			t.Fatalf("Undo left relocation path unowned: %s %v", path, err)
		}
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("Undo changed source before preparation completed: %v", err)
	}
	close(proceed)
	released = true
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(media); err != nil || string(got) != "original audio" {
		t.Fatalf("Undo lost media: %q %v", got, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("Undo retained destination: %v", err)
	}
	for _, path := range []string{source, destination} {
		lease, err := intakelease.Observe(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
