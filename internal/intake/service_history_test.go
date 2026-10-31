package intake

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestServicePreparationOverlapsCollectionOwner(t *testing.T) {
	_, lib := queueFixture(t)
	importCollectionFixture(t, lib)
	service, err := OpenService(t.Context(), lib, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := service.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = history.Run(t.Context(), lib, "test.collection", history.ScopeCollection, nil, func(context.Context) error {
		// Process uses the real worker and the real SQLite History owner.
		// An ordinary collection owner is not damaged import evidence.
		if err := service.Process(t.Context()); err != nil {
			t.Errorf("ordinary collection ownership became worker failure: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Process(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code := service.ErrorCode(); code != "" {
		t.Fatalf("successful preparation retained failure: %s", code)
	}
}

func TestServiceReconcilesFreshReceiptAfterImportOwner(t *testing.T) {
	for _, closeBeforeCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "observe committed tuple", true: "close joins cancelled native wait"}[closeBeforeCommit], func(t *testing.T) {
			testServiceReconcilesFreshReceipt(t, closeBeforeCommit)
		})
	}
}

func testServiceReconcilesFreshReceipt(t *testing.T, closeBeforeCommit bool) {
	t.Helper()
	s, lib := queueFixture(t)
	database := importCollectionFixture(t, lib)
	source := writeImportWAV(t)
	item, err := s.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareNext(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{ID: item.ID}
	review, err := s.ReviewImport(t.Context(), database, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = review.PlanToken
	writer, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.Exec("UPDATE Track SET title=title"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	imported := make(chan error, 1)
	go func() {
		_, err := history.Run(ctx, lib, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
			_, err := s.ApplyImport(ctx, database, opts)
			return err
		})
		imported <- err
	}()
	waitQueueStatus(t, s, item.ID, "importing")
	var trackID sql.NullInt64
	if err := s.database.QueryRow("SELECT track_id FROM ImportIntent WHERE item_id=?", item.ID).Scan(&trackID); err != nil || trackID.Valid {
		t.Fatalf("native writer did not hold incomplete receipt: %+v %v", trackID, err)
	}
	service, err := OpenService(ctx, lib, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	ready := make(chan error, 1)
	go func() { ready <- service.Ready(ctx) }()
	select {
	case err := <-ready:
		t.Fatalf("incomplete importing owner became observed receipt: %v code=%s", err, service.ErrorCode())
	case <-time.After(30 * time.Millisecond):
	}
	if !closeBeforeCommit {
		// Cancelling individual callers must leave the shared worker alive and
		// waiting for the real importing owner to publish its complete tuple.
		for _, wait := range []struct {
			name string
			run  func(context.Context) error
		}{{"readiness", service.Ready}, {"preparation", service.Process}} {
			t.Run("cancel caller "+wait.name, func(t *testing.T) {
				caller, cancelCaller := context.WithCancel(ctx)
				defer cancelCaller()
				result := make(chan error, 1)
				go func() { result <- wait.run(caller) }()
				select {
				case err := <-result:
					t.Fatalf("caller passed unpublished import: %v", err)
				case <-time.After(30 * time.Millisecond):
				}
				cancelCaller()
				if err := <-result; !errors.Is(err, context.Canceled) {
					t.Fatalf("caller lost cancellation: %v", err)
				}
				called := false
				if err := service.Use(caller, func(*Store) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
					t.Fatalf("cancelled caller used shared storage: %v called=%t", err, called)
				}
			})
		}
	}
	if closeBeforeCommit {
		// The importer still owns History and the Engine writer remains held.
		// Closing must interrupt the observation instead of waiting for either.
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-ready; err == nil {
			t.Fatal("closed observer reported readiness before publication")
		}
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-imported; err != nil {
		t.Fatal(err)
	}
	if closeBeforeCommit {
		service, err = OpenService(ctx, lib, 0)
		if err != nil {
			t.Fatal(err)
		}
		ready = make(chan error, 1)
		go func() { ready <- service.Ready(ctx) }()
	}
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	if err := service.Process(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 || items[0].Status != "imported" || items[0].HistoryID == "" || service.ErrorCode() != "" {
		t.Fatalf("fresh committed receipt not reconciled: %+v %v code=%s", items, err, service.ErrorCode())
	}
	control, err := s.Control(ctx)
	if err != nil || control.Paused {
		t.Fatalf("fresh Track result was quarantined from stale NULL: %+v %v", control, err)
	}
	if data, err := os.ReadFile(source); err != nil || len(data) != 96044 {
		t.Fatalf("import changed original source: %d %v", len(data), err)
	}
}
