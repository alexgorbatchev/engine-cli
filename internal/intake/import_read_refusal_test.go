package intake

import (
	"os"
	"testing"
)

func TestImportReviewRefusesUnavailableQueueState(t *testing.T) {
	for _, test := range []struct {
		name, damage string
		missing      bool
	}{
		{name: "missing item", missing: true},
		{name: "not staged"},
		{name: "unreadable control", damage: "ALTER TABLE QueueControl RENAME TO RetainedControl"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, library := queueFixture(t)
			database := importCollectionFixture(t, library)
			source := writeImportWAV(t)
			item, err := s.Enqueue(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			if test.damage != "" {
				if _, err := s.database.Exec(test.damage); err != nil {
					t.Fatal(err)
				}
			}
			id := item.ID
			if test.missing {
				id++
			}
			if review, err := s.ReviewImport(t.Context(), database, ImportOptions{ID: id}); review != nil || err == nil {
				t.Fatalf("unavailable queue state issued review: %+v %v", review, err)
			}
			var tracks, intents int
			if err := database.QueryRow("SELECT count(*) FROM Track").Scan(&tracks); err != nil {
				t.Fatal(err)
			}
			if err := s.database.QueryRow("SELECT count(*) FROM ImportIntent").Scan(&intents); err != nil {
				t.Fatal(err)
			}
			if tracks != 0 || intents != 0 {
				t.Fatalf("refused review wrote tracks=%d intents=%d", tracks, intents)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReconcileRefusesUnreadableReceiptProjection(t *testing.T) {
	for _, damage := range []string{
		"ALTER TABLE ImportIntent RENAME TO RetainedIntent",
		"ALTER TABLE ImportIntent RENAME TO RetainedIntent; CREATE VIEW ImportIntent AS SELECT NULL AS item_id,'unreadable' AS history_id,NULL AS track_id",
	} {
		t.Run(damage, func(t *testing.T) {
			s, library := queueFixture(t)
			database := importCollectionFixture(t, library)
			if err := s.ReconcileImports(t.Context()); err != nil {
				t.Fatal(err)
			}
			item, err := s.Enqueue(t.Context(), writeImportWAV(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.database.Exec(damage); err != nil {
				t.Fatal(err)
			}
			if err := s.ReconcileImports(t.Context()); err == nil {
				t.Fatal("unreadable receipt projection treated as reconciled")
			}
			items, err := s.List(t.Context())
			if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Status != "queued" || items[0].HistoryID != "" {
				t.Fatalf("failed reconciliation changed ownership: %+v %v", items, err)
			}
			var tracks int
			if err := database.QueryRow("SELECT count(*) FROM Track").Scan(&tracks); err != nil || tracks != 0 {
				t.Fatalf("failed reconciliation wrote tracks=%d: %v", tracks, err)
			}
		})
	}
}
