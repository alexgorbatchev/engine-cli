package intake

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestPausePersistsUntilExplicitResume(t *testing.T) {
	s, library := queueFixture(t)
	item, err := s.Enqueue(t.Context(), sourceFixture(t, "incoming.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state, err := reopened.Control(t.Context())
	if err != nil || !state.Paused || state.Reason != "manual" {
		t.Fatalf("pause lost across reopen: %+v, %v", state, err)
	}
	claimed, err := reopened.Claim(t.Context())
	if err != nil || claimed != nil {
		t.Fatalf("paused direct claim: %+v, %v", claimed, err)
	}
	prepared, err := reopened.PrepareNext(t.Context(), 0)
	if err != nil || prepared != nil {
		t.Fatalf("paused preparation: %+v, %v", prepared, err)
	}
	if err := reopened.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	prepared, err = reopened.PrepareNext(t.Context(), 0)
	if err != nil || prepared == nil || prepared.ID != item.ID || prepared.Attempt != 1 || prepared.Status != "staged" {
		t.Fatalf("explicit resume lost FIFO source: %+v, %v", prepared, err)
	}
}

func TestMissingHistoryQuarantinesCommittedReceiptAcrossRestart(t *testing.T) {
	s, library := queueFixture(t)
	d := importCollectionFixture(t, library)
	source := writeImportWAV(t)
	item, err := s.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareNext(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{ID: item.ID}
	review, err := s.ReviewImport(t.Context(), d, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = review.PlanToken
	entry, err := history.Run(t.Context(), library, "intake.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := s.ApplyImport(ctx, d, opts)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileImports(t.Context()); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Enqueue(t.Context(), sourceFixture(t, "later.wav"))
	if err != nil {
		t.Fatal(err)
	}
	// Portable queue images can arrive without the separate History journal.
	// Preserve the genuine committed journal as evidence, rather than inventing
	// a receipt or inferring success from the destination path.
	journal := filepath.Join(library, ".engine-cli", "history")
	retained := filepath.Join(t.TempDir(), "retained-history")
	if err := os.Rename(journal, retained); err != nil {
		t.Fatal(err)
	}
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for service.ErrorCode() != "intake_import_recovery_required" {
		select {
		case <-ctx.Done():
			t.Fatal("missing History did not produce a completed recovery observation")
		case <-ticker.C:
		}
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := s.Control(t.Context())
	if err != nil || !state.Paused || state.Reason != "history_unavailable" {
		t.Fatalf("missing History was not durably quarantined: %+v, %v", state, err)
	}
	if err := s.Resume(t.Context()); progress.ErrorCode(err) != "intake_import_recovery_required" {
		t.Fatalf("unproven foreign receipt resumed: %v", err)
	}
	prepared, err := s.PrepareNext(t.Context(), 0)
	if err != nil || prepared != nil {
		t.Fatalf("quarantined queue prepared machine-local source: %+v, %v", prepared, err)
	}
	again, err := s.Enqueue(t.Context(), source)
	if err != nil || again.ID != item.ID || again.Status != "imported" || again.HistoryID != entry.ID {
		t.Fatalf("unavailable History lost committed dedup receipt: %+v, %v", again, err)
	}
	var tracks int
	if err := d.QueryRow(`SELECT count(*) FROM Track`).Scan(&tracks); err != nil || tracks != 1 {
		t.Fatalf("quarantine changed committed collection: %d, %v", tracks, err)
	}
	if err := os.Rename(retained, journal); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	prepared, err = s.PrepareNext(t.Context(), 0)
	if err != nil || prepared == nil || prepared.ID != queued.ID || prepared.Status != "staged" {
		t.Fatalf("restored exact ownership could not resume later FIFO item: %+v, %v", prepared, err)
	}
}
