package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIIntakeSyncKeepsQueuesLibraryLocal(t *testing.T) {
	s, _ := testAPI(t)
	source, destination := dbtest.Library(t, "source"), dbtest.Library(t, "destination")
	s.intake.quiet = time.Hour
	var owners []*intake.Service
	var paths []string
	for _, lib := range []string{source, destination} {
		path := filepath.Join(t.TempDir(), "local.wav")
		if err := os.WriteFile(path, []byte(lib), 0600); err != nil {
			t.Fatal(err)
		}
		q, err := intake.Open(t.Context(), lib)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.Enqueue(t.Context(), path); err != nil {
			t.Fatal(err)
		}
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		owner, err := s.intake.open(lib, false)
		if err != nil {
			t.Fatal(err)
		}
		waitIntakeItem(t, owner, 1, "preparing")
		owners, paths = append(owners, owner), append(paths, path)
	}
	params := map[string]any{"source": source, "destination": destination, "scope": map[string]bool{"root": true}, "dry_run": true}
	preview, _, err := s.RunOperation(t.Context(), "library.sync", mustJSON(t, params))
	if err != nil {
		t.Fatal(err)
	}
	// Preview leaves both preparation owners alive; apply joins destination
	// before waiting for its collection gate, without taking the source worker.
	for _, owner := range owners {
		if err := owner.Use(t.Context(), func(*intake.Store) error { return nil }); err != nil {
			t.Fatalf("preview closed queue: %v", err)
		}
	}
	encoded := mustJSON(t, preview)
	var plan struct {
		PlanToken string `json:"plan_token"`
	}
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatal(err)
	}
	params["dry_run"], params["plan_token"] = false, plan.PlanToken
	if err := s.collection.Acquire(t.Context(), collectionCapacity); err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			s.collection.Release(collectionCapacity)
		}
	}()
	w := requestAPI(t, s, http.MethodPost, "/v1/operations/library.sync", string(mustJSON(t, params)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("sync: %d %s", w.Code, w.Body)
	}
	var job Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for progress.ErrorCode(owners[1].Use(ctx, func(*intake.Store) error { return nil })) != "intake_closed" {
		select {
		case <-ctx.Done():
			t.Fatal("sync did not stop destination before scheduler")
		case <-tick.C:
		}
	}
	if err := owners[0].Use(t.Context(), func(*intake.Store) error { return nil }); err != nil {
		t.Fatalf("sync stopped source: %v", err)
	}
	s.collection.Release(collectionCapacity)
	held = false
	if result := awaitAPIJob(t, s, job.ID); result["state"] != "succeeded" {
		t.Fatalf("sync failed: %+v", result)
	}
	for i, lib := range []string{source, destination} {
		owner, err := s.intake.open(lib, false)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && owner == owners[1] {
			t.Fatal("sync retained destination storage owner")
		}
		if err := owner.Use(t.Context(), func(q *intake.Store) error {
			items, err := q.List(t.Context())
			if err != nil {
				return err
			}
			if len(items) != 1 || items[0].SourcePath != paths[i] {
				t.Fatalf("sync copied foreign queue: %+v", items)
			}
			control, err := q.Control(t.Context())
			if err == nil && control.Paused {
				t.Fatalf("local queue unexpectedly quarantined: %+v", control)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}
