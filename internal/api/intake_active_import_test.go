package api

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestAPIIntakeRestoreJoinsActualImport(t *testing.T) {
	s, lib := testAPI(t)
	testAPIIntakeRestoreJoinsActualImport(t, s, lib)
}

func testAPIIntakeRestoreJoinsActualImport(t *testing.T, s *Server, lib string) {
	t.Helper()
	registryPath, err := intakeLibraryPath(lib)
	if err != nil {
		t.Fatal(err)
	}
	s.intake.quiet = 0
	source := intakeAPISource(t)
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "intake.enqueue", mustJSON(t, intakePaths{Paths: []string{source}})); err != nil {
		t.Fatal(err)
	}
	service, err := s.intake.open(lib, false)
	if err != nil {
		t.Fatal(err)
	}
	waitIntakeItem(t, service, 1, "staged")
	value, _, err := s.RunOperation(t.Context(), "intake.import.review", mustJSON(t, intakeImport{ID: 1}))
	if err != nil {
		t.Fatal(err)
	}
	review := value.(*staging.ImportReview)
	snapshot, err := backup.CreateBackup(t.Context(), lib, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	writer, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	// Reserve the actual native collection writer without changing the token's
	// committed image. Reads and History snapshots remain available in WAL.
	if _, err := writer.ExecContext(t.Context(), "UPDATE Track SET title=title"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	importBody := mustJSON(t, intakeImport{ID: 1, PlanToken: review.PlanToken})
	restoreBody := mustJSON(t, map[string]string{"path": snapshot.Path})
	imported := make(chan error, 1)
	go func() {
		_, _, err := s.RunOperation(ctx, "intake.import", importBody)
		imported <- err
	}()
	// The durable intent establishes that the real import callback is active;
	// this is not merely a queued scheduler request or a quiet-time sleeper.
	waitIntakeItem(t, service, 1, "importing")
	restored := make(chan error, 1)
	go func() {
		_, _, err := s.RunOperation(ctx, "library.restore", restoreBody)
		restored <- err
	}()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.intake.mu.Lock()
		entry := s.intake.entries[registryPath]
		blocked := entry != nil && entry.blocked
		s.intake.mu.Unlock()
		if entry == nil {
			t.Fatal("active import has no intake registry entry at the observed library path")
		}
		if blocked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("replacement did not enter service join")
		case <-tick.C:
		}
	}
	select {
	case err := <-restored:
		t.Fatalf("replacement bypassed actual active import: %v", err)
	default:
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	for name, done := range map[string]<-chan error{"import": imported, "restore": restored} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s failed: %v", name, err)
			}
		case <-ctx.Done():
			t.Fatalf("%s deadlocked joining import under collection ownership", name)
		}
	}
	if err := service.Use(t.Context(), func(*intake.Store) error { return nil }); progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("old queue remained writable after replacement: %v", err)
	}
	view := readAPIIntake(t, s)
	if !view.Control.Paused || view.Control.Reason != "restored" || len(view.Items) != 1 || view.Items[0].Status != "staged" || view.Items[0].HistoryID != "" {
		t.Fatalf("restore did not expose replacement queue: %+v", view)
	}
	if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("source changed during import/replacement: %d %v", len(data), err)
	}
}
