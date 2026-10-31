package intake

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestImportCompletionStorageRefusalRetainsOwnership(t *testing.T) {
	for _, ignoredReceipt := range []bool{false, true} {
		name := "reconciliation update refused"
		if ignoredReceipt {
			name = "track receipt ignored"
		}
		t.Run(name, func(t *testing.T) {
			s, library := queueFixture(t)
			database := importCollectionFixture(t, library)
			queued, err := s.Enqueue(t.Context(), writeImportWAV(t))
			if err != nil {
				t.Fatal(err)
			}
			if staged, err := s.PrepareNext(t.Context(), 0); err != nil || staged == nil {
				t.Fatalf("prepare: %+v %v", staged, err)
			}
			opts := ImportOptions{ID: queued.ID}
			review, err := s.ReviewImport(t.Context(), database, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = review.PlanToken
			if ignoredReceipt {
				if _, err := s.database.Exec("CREATE TRIGGER ignore_receipt BEFORE UPDATE OF track_id ON ImportIntent BEGIN SELECT RAISE(IGNORE); END"); err != nil {
					t.Fatal(err)
				}
			}
			entry, err := history.Run(t.Context(), library, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
				_, err := s.ApplyImport(ctx, database, opts)
				return err
			})
			if ignoredReceipt {
				if entry == nil || entry.State != "pending" || progress.ErrorCode(err) != "history_recovery_required" {
					t.Fatalf("ignored receipt lost recovery ownership: %+v %v", entry, err)
				}
			} else {
				if err != nil || entry == nil || entry.State != "complete" {
					t.Fatalf("committed import: %+v %v", entry, err)
				}
				if _, err := s.database.Exec("CREATE TRIGGER refuse_finalize BEFORE UPDATE OF status ON Item BEGIN SELECT RAISE(ABORT,'queue storage unavailable'); END"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ReconcileImports(t.Context()); err == nil {
				t.Fatal("incomplete queue finalization reported success")
			}
			items, err := s.List(t.Context())
			if err != nil || len(items) != 1 || items[0].Status != "importing" || items[0].HistoryID != entry.ID {
				t.Fatalf("failed completion lost ownership: %+v %v", items, err)
			}
			var tracks int
			if err := database.QueryRow("SELECT count(*) FROM Track").Scan(&tracks); err != nil || tracks != 1 {
				t.Fatalf("committed track lost: %d %v", tracks, err)
			}
			if _, err := s.Retry(t.Context(), queued.ID); err == nil {
				t.Fatal("owned committed import became retryable")
			}
		})
	}
}
