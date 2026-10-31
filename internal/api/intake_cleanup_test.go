package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIIntakeCleanupFailureRetainsCommittedMutation(t *testing.T) {
	s, lib := testAPI(t)
	file, err := os.Create(filepath.Join(t.TempDir(), "cleanup"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	op := s.operations["playlist.create"]
	bound, err := op.bind(mustJSON(t, map[string]string{"name": "Committed before cleanup"}))
	if err != nil {
		t.Fatal(err)
	}
	var terminals []progress.Event
	ctx := progress.WithReporter(t.Context(), func(e progress.Event) {
		if e.Type == "complete" || e.Type == "error" || e.Type == "cancelled" {
			terminals = append(terminals, e)
		}
	})
	result, id, err := s.executeOwned(ctx, op, bound, func(ctx context.Context) (func() error, error) {
		release, err := s.acquireOperation(ctx, op, bound)
		if err != nil {
			return nil, err
		}
		return func() error {
			release()
			// Reopening intake may need this owner; cleanup must not retain it.
			if !s.collection.TryAcquire(collectionCapacity) {
				t.Error("scheduler still held during cleanup")
			} else {
				s.collection.Release(collectionCapacity)
			}
			// Real native file-cleanup failure, not an induced lease.Close failure.
			return file.Close()
		}, nil
	})
	if !errors.Is(err, os.ErrClosed) || result == nil || id == "" {
		t.Fatalf("lost committed result/history or cleanup failure: %v %s %v", result, id, err)
	}
	if len(terminals) != 1 || terminals[0].Type != "error" {
		t.Fatalf("cleanup emitted wrong terminal sequence: %+v", terminals)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("committed history was lost: %+v %v", entries, err)
	}
	if _, err := history.Undo(t.Context(), lib, id, history.UndoOptions{}); err != nil {
		t.Fatalf("committed mutation cannot be undone: %v", err)
	}
}

func TestAPIIntakeCleanupAcquisitionErrorAndCancellation(t *testing.T) {
	s, _ := testAPI(t)
	op := s.operations["playlist.list"]
	bound, err := op.bind(mustJSON(t, emptyParams{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquisition refusal", true: "cancelled before ownership"}[cancelled], func(t *testing.T) {
			var terminals []progress.Event
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			ctx = progress.WithReporter(ctx, func(e progress.Event) {
				if e.Type == "complete" || e.Type == "error" || e.Type == "cancelled" {
					terminals = append(terminals, e)
				}
			})
			acquired, cleaned := false, false
			refused := &progress.Error{Code: "intake_replacing", Cause: errors.New("library replacement owns intake")}
			_, _, err := s.executeOwned(ctx, op, bound, func(context.Context) (func() error, error) {
				acquired = true
				return func() error { cleaned = true; return nil }, refused
			})
			kind := "error"
			if cancelled {
				kind = "cancelled"
				if !errors.Is(err, context.Canceled) || acquired || cleaned {
					t.Fatalf("cancelled ownership: %v %v %v", err, acquired, cleaned)
				}
			} else if !errors.Is(err, refused) || !acquired || !cleaned {
				t.Fatalf("refused ownership cleanup: %v %v %v", err, acquired, cleaned)
			}
			if len(terminals) != 1 || terminals[0].Type != kind {
				t.Fatalf("wrong queued terminal: %+v", terminals)
			}
		})
	}
}

func TestAPIIntakeClosedManagerReportsFailure(t *testing.T) {
	s, _ := testAPI(t)
	if err := s.intake.close(); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "intake.process", `{}`))
	failure, ok := job["error"].(map[string]any)
	if job["state"] != "failed" || !ok || failure["code"] != "intake_closed" {
		t.Fatalf("closed queue manager did not report native refusal: %+v", job)
	}
}

func TestAPIIntakePausedReviewReportsFailure(t *testing.T) {
	s, _ := testAPI(t)
	if _, _, err := s.RunOperation(t.Context(), "intake.pause", mustJSON(t, emptyParams{})); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "intake.import.review", `{"id":1}`))
	failure, ok := job["error"].(map[string]any)
	if job["state"] != "failed" || !ok || failure["code"] != "intake_paused" {
		t.Fatalf("paused review did not report native refusal: %+v", job)
	}
}

func TestAPIIntakeOwnershipReleaseCanLeaveWorkerStopped(t *testing.T) {
	s, lib := testAPI(t)
	if service, err := s.intake.open(lib, true); err != nil || service == nil {
		t.Fatalf("open queue owner: %v", err)
	}
	finish, err := s.intake.stop(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(false); err != nil {
		t.Fatal(err)
	}
	// Failed native release must not implicitly create another observation.
	replacement, err := intakelease.Replace(t.Context(), lib)
	if err != nil {
		t.Fatalf("cleanup reopened worker after failed release: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	// Explicit recovery can retry after ownership has actually been released.
	if service, err := s.intake.open(lib, false); err != nil || service == nil {
		t.Fatalf("explicit queue recovery failed: %v", err)
	}
}
