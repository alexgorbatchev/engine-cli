package intake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestImportIntentRefusalDoesNotInstallOrConsumeSource(t *testing.T) {
	for _, test := range []struct{ name, trigger string }{
		{"update refused", "CREATE TRIGGER refuse_intent BEFORE UPDATE OF history_id ON Item BEGIN SELECT RAISE(ABORT,'intent unavailable'); END"},
		{"update ignored", "CREATE TRIGGER refuse_intent BEFORE UPDATE OF history_id ON Item BEGIN SELECT RAISE(IGNORE); END"},
		{"receipt refused", "CREATE TRIGGER refuse_intent BEFORE INSERT ON ImportIntent BEGIN SELECT RAISE(ABORT,'receipt unavailable'); END"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, library := queueFixture(t)
			database := importCollectionFixture(t, library)
			source := writeImportWAV(t)
			queued, err := store.Enqueue(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			if item, err := store.PrepareNext(t.Context(), 0); err != nil || item == nil || item.Status != "staged" {
				t.Fatalf("preparation: %+v %v", item, err)
			}
			opts := ImportOptions{ID: queued.ID}
			review, err := store.ReviewImport(t.Context(), database, opts)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = review.PlanToken
			if _, err := store.database.ExecContext(t.Context(), test.trigger); err != nil {
				t.Fatal(err)
			}
			_, err = history.Run(t.Context(), library, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
				receipt, err := store.ApplyImport(ctx, database, opts)
				if receipt != nil {
					t.Errorf("refused import issued receipt: %+v", receipt)
				}
				return err
			})
			if err == nil {
				t.Fatal("import without durable intent succeeded")
			}
			items, err := store.List(t.Context())
			if err != nil || len(items) != 1 || items[0].Status != "staged" || items[0].HistoryID != "" {
				t.Fatalf("refused intent changed queue: %+v %v", items, err)
			}
			var count int
			if err := database.QueryRow("SELECT count(*) FROM Track").Scan(&count); err != nil || count != 0 {
				t.Fatalf("refused intent installed track: %d %v", count, err)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("source consumed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(library, filepath.FromSlash(review.Items[0].Destination))); !os.IsNotExist(err) {
				t.Fatalf("refused intent installed destination: %v", err)
			}
		})
	}
}
