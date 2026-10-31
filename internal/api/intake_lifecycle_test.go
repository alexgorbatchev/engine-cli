package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIIntakeRestoreStopsBeforeScheduler(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP job", true: "direct operation"}[direct], func(t *testing.T) {
			testIntakeRestoreStopsBeforeScheduler(t, direct)
		})
	}
}

func testIntakeRestoreStopsBeforeScheduler(t *testing.T, direct bool) {
	t.Helper()
	s, lib := testAPI(t)
	s.intake.quiet = time.Hour
	source := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(source, []byte("original source retained"), 0600); err != nil {
		t.Fatal(err)
	}
	queue, err := intake.Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	item, err := queue.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := backup.CreateBackup(t.Context(), lib, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := s.intake.open(lib, false)
	if err != nil || service == nil {
		t.Fatalf("start enrolled queue: %v", err)
	}
	waitIntakeItem(t, service, item.ID, "preparing")
	// Hold the actual API scheduler. Restore must still close its worker first,
	// rather than acquiring this owner and then joining an import needing it.
	if err := s.collection.Acquire(t.Context(), collectionCapacity); err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			s.collection.Release(collectionCapacity)
		}
	}()
	body, err := json.Marshal(map[string]string{"path": snapshot.Path})
	if err != nil {
		t.Fatal(err)
	}
	var accepted Job
	completed := make(chan error, 1)
	if direct {
		go func() {
			_, _, err := s.RunOperation(t.Context(), "library.restore", body)
			completed <- err
		}()
	} else {
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/library.restore", string(body))
		if w.Code != http.StatusAccepted {
			t.Fatalf("submit: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		err := service.Use(ctx, func(*intake.Store) error { return nil })
		if progress.ErrorCode(err) == "intake_closed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("restore did not join intake before scheduler acquisition")
		case <-ticker.C:
		}
	}
	if _, err := s.intake.open(lib, true); progress.ErrorCode(err) != "intake_replacing" {
		t.Fatalf("queue reopened during replacement: %v", err)
	}
	s.collection.Release(collectionCapacity)
	held = false
	if direct {
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("direct restore did not finish")
		}
	} else {
		job := awaitAPIJob(t, s, accepted.ID)
		if job["state"] != "succeeded" {
			t.Fatalf("restore failed: %+v", job)
		}
	}
	reopened, err := s.intake.open(lib, false)
	if err != nil || reopened == nil || reopened == service {
		t.Fatalf("replacement did not reopen fresh owner: %p %v", reopened, err)
	}
	if err := reopened.Use(t.Context(), func(q *intake.Store) error {
		control, err := q.Control(t.Context())
		if err != nil || !control.Paused || control.Reason != "restored" {
			t.Fatalf("restored source paths activated: %+v %v", control, err)
		}
		items, err := q.List(t.Context())
		if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Status != "queued" {
			t.Fatalf("replacement used stale storage: %+v %v", items, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "original source retained" {
		t.Fatalf("source changed: %q %v", data, err)
	}
}

func waitIntakeItem(t *testing.T, service *intake.Service, id int64, status string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		found := false
		if err := service.Use(ctx, func(q *intake.Store) error {
			items, err := q.List(ctx)
			for _, item := range items {
				if item.ID == id && item.Status == status {
					found = true
				}
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("item %d never reached %s", id, status)
		case <-ticker.C:
		}
	}
}

func TestAPIIntakeControlAndRestart(t *testing.T) {
	s, lib := testAPI(t)
	s.intake.quiet = time.Hour
	source := filepath.Join(t.TempDir(), "incoming.wav")
	if err := os.WriteFile(source, []byte("source survives queue controls"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(intakePaths{Paths: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "intake.enqueue", string(body))
	service, err := s.intake.open(lib, false)
	if err != nil || service == nil {
		t.Fatalf("enqueue did not own queue: %v", err)
	}
	view := readAPIIntake(t, s)
	if len(view.Items) != 1 {
		t.Fatalf("queue lost source: %+v", view)
	}
	id := view.Items[0].ID
	waitIntakeItem(t, service, id, "preparing")
	runAPIJob(t, s, "intake.pause", "{}")
	view = readAPIIntake(t, s)
	if !view.Control.Paused || view.Control.Reason != "manual" || view.WorkerErrorCode != "" || view.RecoveryErrorCode != "" {
		t.Fatalf("pause became a worker failure: %+v", view)
	}
	s.Close()
	restarted, err := New(Config{LibraryDir: lib, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	view = readAPIIntake(t, restarted)
	if !view.Control.Paused || view.Items[0].ID != id || view.Items[0].Attempt != 1 {
		t.Fatalf("restart resumed paused source: %+v", view)
	}
	runAPIJob(t, restarted, "intake.resume", "{}")
	resumed, err := restarted.intake.open(lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitIntakeItem(t, resumed, id, "staged")
	view = readAPIIntake(t, restarted)
	if view.Control.Paused || len(view.Items) != 1 || view.Items[0].Attempt != 2 || view.Items[0].ContentHash == "" {
		t.Fatalf("resume did not finish retained item: %+v", view)
	}
	idBody, err := json.Marshal(intakeID{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, restarted, "intake.cancel", string(idBody))
	if got := readAPIIntake(t, restarted); got.Items[0].Status != "cancelled" {
		t.Fatalf("cancel not durable: %+v", got)
	}
	runAPIJob(t, restarted, "intake.retry", string(idBody))
	waitIntakeItem(t, resumed, id, "staged")
	if data, err := os.ReadFile(source); err != nil || string(data) != "source survives queue controls" {
		t.Fatalf("queue controls changed source: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("operational queue controls enrolled collection History: %v", err)
	}
}

func readAPIIntake(t *testing.T, s *Server) IntakeView {
	t.Helper()
	job := runAPIJob(t, s, "intake.list", "{}")
	data, err := json.Marshal(job["result"])
	if err != nil {
		t.Fatal(err)
	}
	var view IntakeView
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestAPIIntakeRecoveryDoesNotCloseLibrary(t *testing.T) {
	s, lib := testAPI(t)
	queue, err := intake.Open(t.Context(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	path := filepath.Join(lib, ".engine-cli", "intake.db")
	retained := filepath.Join(t.TempDir(), "retained-intake.db")
	if err := os.Rename(path, retained); err != nil {
		t.Fatal(err)
	}
	const damaged = "damaged intake storage, not a library failure"
	if err := os.WriteFile(path, []byte(damaged), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Config{LibraryDir: lib, NoBackup: true})
	if err != nil {
		t.Fatalf("queue recovery made library inaccessible: %v", err)
	}
	t.Cleanup(restarted.Close)
	view := readAPIIntake(t, restarted)
	if view.RecoveryErrorCode != "intake_recovery_required" || len(view.Items) != 0 {
		t.Fatalf("queue failure not discoverable: %+v", view)
	}
	runAPIJob(t, restarted, "track.list", "{}")
	if data, err := os.ReadFile(path); err != nil || string(data) != damaged {
		t.Fatalf("damaged evidence overwritten: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retained, path); err != nil {
		t.Fatal(err)
	}
	view = readAPIIntake(t, restarted)
	if view.RecoveryErrorCode != "" || view.WorkerErrorCode != "" {
		t.Fatalf("restored queue did not retry open: %+v", view)
	}
}
