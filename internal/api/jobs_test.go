package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func submitAPIJob(t *testing.T, s *Server, name, body string) string {
	t.Helper()
	w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+name, body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body)
	}
	var job Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach synchronization point")
	}
}

func TestAPIQueuedCancellationAndShutdown(t *testing.T) {
	s, _ := testAPI(t)
	started := make(chan struct{})
	exited := make(chan struct{})
	s.operations["test.block"] = define("test.block", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		close(started)
		defer close(exited)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	active := submitAPIJob(t, s, "test.block", `{}`)
	waitSignal(t, started)
	queued := submitAPIJob(t, s, "playlist.create", `{"name":"Must not execute"}`)
	w := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+queued+"/cancel", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("cancel=%d %s", w.Code, w.Body)
	}
	job := awaitAPIJob(t, s, queued)
	if job["state"] != "cancelled" {
		t.Fatalf("queued cancel=%+v", job)
	}
	s.Close()
	waitSignal(t, exited)
	if job := awaitAPIJob(t, s, active); job["state"] != "cancelled" {
		t.Fatalf("shutdown job=%+v", job)
	}
	w = requestAPI(t, s, http.MethodPost, "/v1/operations/playlist.list", `{}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed server accepted work: %d %s", w.Code, w.Body)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/jobs/missing/cancel", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing cancel=%d", w.Code)
	}
}

func TestAPIJobCapacityAndEviction(t *testing.T) {
	t.Run("active capacity", func(t *testing.T) {
		s, _ := testAPI(t)
		started := make(chan struct{})
		s.operations["test.block"] = define("test.block", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		submitAPIJob(t, s, "test.block", `{}`)
		waitSignal(t, started)
		for range maxJobs - 1 {
			submitAPIJob(t, s, "playlist.list", `{}`)
		}
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/playlist.list", `{}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("capacity status=%d body=%s", w.Code, w.Body)
		}
		s.Close()
	})
	t.Run("terminal eviction", func(t *testing.T) {
		s, _ := testAPI(t)
		first := ""
		for range maxJobs + 1 {
			id := submitAPIJob(t, s, "playlist.list", `{}`)
			if first == "" {
				first = id
			}
			job := awaitAPIJob(t, s, id)
			if job["state"] != "succeeded" {
				t.Fatalf("read failed=%+v", job)
			}
		}
		if w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+first, ""); w.Code != http.StatusNotFound {
			t.Fatalf("oldest job retained: %d", w.Code)
		}
	})
}

func TestAPIEventRetentionAndInvalidCursors(t *testing.T) {
	s, _ := testAPI(t)
	s.operations["test.events"] = define("test.events", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		for i := range maxEvents + 10 {
			progress.Emit(ctx, progress.Event{Phase: "items", Percent: 90 * float64(i) / float64(maxEvents+10)})
		}
		return nil, nil
	})
	id := submitAPIJob(t, s, "test.events", `{}`)
	awaitAPIJob(t, s, id)
	for _, tt := range []struct {
		cursor string
		status int
	}{{"1", http.StatusConflict}, {"-1", http.StatusBadRequest}, {"invalid", http.StatusBadRequest}, {"9999999", http.StatusBadRequest}} {
		w := requestAPIWithCursor(t, s, id, tt.cursor)
		if w.Code != tt.status {
			t.Fatalf("cursor=%s status=%d body=%s", tt.cursor, w.Code, w.Body)
		}
	}
	if w := requestAPI(t, s, http.MethodGet, "/v1/jobs/missing/events", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing events=%d", w.Code)
	}
}

func TestAPITerminalProgressWaitsForFinalJobState(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := testAPI(t)
			emitted := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			s.operations["test.atomic"] = define("test.atomic", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
				typeName := "complete"
				percent := 100.0
				var failure string
				if fail {
					typeName = "error"
					percent = 99
					failure = "operation failed after reporting"
				}
				progress.Emit(ctx, progress.Event{Type: typeName, Percent: percent, Error: failure})
				close(emitted)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if fail {
					return nil, errors.New("operation failed after reporting")
				}
				return map[string]string{"value": "ready"}, nil
			})
			id := submitAPIJob(t, s, "test.atomic", `{}`)
			waitSignal(t, emitted)
			w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id, "")
			var running Job
			if err := json.Unmarshal(w.Body.Bytes(), &running); err != nil {
				t.Fatal(err)
			}
			if running.State != "running" || running.Progress.Type == "complete" || running.Progress.Type == "error" {
				t.Fatalf("terminal event visible before final state: %+v", running)
			}
			release <- struct{}{}
			finished := awaitAPIJob(t, s, id)
			if fail {
				if finished["state"] != "failed" || finished["error"] == nil {
					t.Fatalf("failed terminal missing: %+v", finished)
				}
			} else {
				result, ok := finished["result"].(map[string]any)
				if finished["state"] != "succeeded" || !ok || result["value"] != "ready" {
					t.Fatalf("completed state/result missing: %+v", finished)
				}
			}
		})
	}
}
