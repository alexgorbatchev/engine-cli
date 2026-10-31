package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIIntakeMoveReservesBothPathsBeforeScheduler(t *testing.T) {
	s, lib := testAPI(t)
	destination := filepath.Join(t.TempDir(), "moved")
	opts := libraryops.Options{Path: lib, Destination: destination, DryRun: true}
	preview, _, err := s.RunOperation(t.Context(), "library.move", mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken = false, preview.(*libraryops.Plan).PlanToken
	if err := s.collection.Acquire(t.Context(), collectionCapacity); err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			s.collection.Release(collectionCapacity)
		}
	}()
	op := s.operations["library.move"]
	bound, err := op.bind(mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	resume, err := s.replacementIntake(t.Context(), op, bound)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if resume != nil {
			if err := resume(); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, path := range []string{lib, destination} {
		lease, err := intakelease.Observe(t.Context(), path)
		if lease != nil {
			lease.Close()
		}
		if lease != nil || progress.ErrorCode(err) != "intake_replacing" {
			t.Fatalf("move did not reserve %s before scheduler: %v", path, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if release, err := s.acquireOperation(ctx, op, bound); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("held scheduler bypassed: %v", err)
	}
	if _, err := os.Stat(lib); err != nil {
		t.Fatal("source changed before scheduler", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination created before scheduler: %v", err)
	}
	s.collection.Release(collectionCapacity)
	held = false
	if err := resume(); err != nil {
		t.Fatal(err)
	}
	resume = nil
	if _, _, err := s.RunOperation(t.Context(), "library.move", mustJSON(t, opts)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lib); !os.IsNotExist(err) {
		t.Fatalf("source retained after move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".engine-cli-intake")); !os.IsNotExist(err) {
		t.Fatalf("parent coordinator copied inside library: %v", err)
	}
	lease, err := intakelease.Observe(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAPIIntakeMoveResumesDestination(t *testing.T) {
	s, lib := testAPI(t)
	s.intake.quiet = time.Hour
	source := filepath.Join(t.TempDir(), "source.wav")
	original := []byte("source survives library relocation")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(t, intakePaths{Paths: []string{source}})
	if _, _, err := s.RunOperation(t.Context(), "intake.enqueue", raw); err != nil {
		t.Fatal(err)
	}
	old, err := s.intake.open(lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitIntakeItem(t, old, 1, "preparing")
	destination := filepath.Join(t.TempDir(), "moved")
	opts := libraryops.Options{Path: lib, Destination: destination, DryRun: true}
	preview, _, err := s.RunOperation(t.Context(), "library.move", mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken = false, preview.(*libraryops.Plan).PlanToken
	s.intake.quiet = 0
	_, historyID, err := s.RunOperation(t.Context(), "library.move", mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Use(t.Context(), func(*intake.Store) error { return nil }); progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("old service survived move: %v", err)
	}
	// Observe durable destination storage directly through its domain, without
	// opening an API route that could lazily start the missing worker itself.
	queue, err := intake.Open(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	waitIntakeStoredStage(t, queue, 1, 1)
	second := filepath.Join(t.TempDir(), "second.wav")
	if err := os.WriteFile(second, original, 0600); err != nil {
		t.Fatal(err)
	}
	item, err := queue.Enqueue(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "library.undo", mustJSON(t, undoParams{Library: destination, ID: historyID})); err != nil {
		t.Fatal(err)
	}
	restored, err := intake.Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	waitIntakeStoredStage(t, restored, item.ID, 2)
	if b, err := os.ReadFile(source); err != nil || string(b) != string(original) {
		t.Fatalf("source changed: %q %v", b, err)
	}
}

func waitIntakeStoredStage(t *testing.T, queue *intake.Store, id int64, count int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		items, err := queue.List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if len(items) == count && item.ID == id && item.Status == "staged" {
				return
			}
		}
		select {
		case <-deadline.C:
			b, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			t.Fatalf("moved queue did not resume: %s", b)
		case <-tick.C:
		}
	}
}
