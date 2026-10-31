package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func queueFixture(t *testing.T) (*Store, string) {
	t.Helper()
	library := t.TempDir()
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, library
}

func sourceFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("source media retained unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestQueuePersistsVersionsAndOrder(t *testing.T) {
	s, library := queueFixture(t)
	ctx := t.Context()
	firstPath := sourceFixture(t, "first.wav")
	first, err := s.Enqueue(ctx, firstPath)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Enqueue(ctx, firstPath)
	if err != nil || again.ID != first.ID {
		t.Fatalf("duplicate discovery = %+v, %v", again, err)
	}
	second, err := s.Enqueue(ctx, sourceFixture(t, "second.wav"))
	if err != nil {
		t.Fatal(err)
	}
	// Native replacement, with identical path/content/mtime, is a new source
	// version. Identity cannot be reduced to a path or metadata tuple.
	info, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(firstPath, firstPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstPath, []byte("source media retained unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(firstPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.Enqueue(ctx, firstPath)
	if err != nil || replacement.ID == first.ID {
		t.Fatalf("replaced source = %+v, %v", replacement, err)
	}
	reopened, err := Open(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	items, err := reopened.List(ctx)
	if err != nil || len(items) != 3 || items[0].ID != first.ID || items[1].ID != second.ID || items[2].ID != replacement.ID {
		t.Fatalf("durable FIFO = %+v, %v", items, err)
	}
	for _, path := range []string{firstPath, firstPath + ".old"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "source media retained unchanged" {
			t.Fatalf("source preservation %s: %q, %v", path, data, err)
		}
	}
}

func TestQueueAttemptAndCancellationFence(t *testing.T) {
	s, _ := queueFixture(t)
	ctx := t.Context()
	first, err := s.Enqueue(ctx, sourceFixture(t, "first.wav"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Enqueue(ctx, sourceFixture(t, "second.wav"))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(ctx)
	if err != nil || claim.ID != first.ID || claim.Attempt != 1 {
		t.Fatalf("first claim = %+v, %v", claim, err)
	}
	if got, err := s.Claim(ctx); err != nil || got != nil {
		t.Fatalf("overlapping claim = %+v, %v", got, err)
	}
	if _, err := s.Cancel(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(ctx, first.ID); progress.ErrorCode(err) != "intake_state_changed" {
		t.Fatalf("retry while worker still owns source = %v", err)
	}
	done, err := s.CompleteStage(ctx, StageResult{ID: first.ID, Attempt: claim.Attempt, ErrorCode: "cancelled"})
	if err != nil || done.Status != "cancelled" {
		t.Fatalf("cancelled worker result = %+v, %v", done, err)
	}
	if _, err := s.Retry(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Claim(ctx)
	if err != nil || retry.ID != first.ID || retry.Attempt != 2 {
		t.Fatalf("retry claim = %+v, %v", retry, err)
	}
	if retry.StageID == "" || retry.StageID != claim.StageID {
		t.Fatalf("retry lost durable staging reservation: %q -> %q", claim.StageID, retry.StageID)
	}
	if _, err := s.CompleteStage(ctx, StageResult{ID: first.ID, Attempt: claim.Attempt, ErrorCode: "operation_failed"}); progress.ErrorCode(err) != "intake_state_changed" {
		t.Fatalf("late completion crossed attempt fence: %v", err)
	}
	if _, err := s.CompleteStage(ctx, StageResult{ID: first.ID, Attempt: retry.Attempt, StageID: "another-stage", ContentHash: "digest"}); progress.ErrorCode(err) != "intake_state_changed" {
		t.Fatalf("foreign staging reservation accepted: %v", err)
	}
	done, err = s.CompleteStage(ctx, StageResult{ID: first.ID, Attempt: retry.Attempt, StageID: retry.StageID, ContentHash: "digest"})
	if err != nil || done.Status != "staged" {
		t.Fatalf("new completion = %+v, %v", done, err)
	}
	next, err := s.Claim(ctx)
	if err != nil || next.ID != second.ID {
		t.Fatalf("next FIFO claim = %+v, %v", next, err)
	}
}

func TestQueueRefusesCancelledAndForeignStorage(t *testing.T) {
	s, library := queueFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if item, err := s.Enqueue(ctx, sourceFixture(t, "cancelled.wav")); item != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled enqueue = %+v, %v", item, err)
	}
	if _, err := s.database.ExecContext(t.Context(), "PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	if foreign, err := Open(t.Context(), library); err == nil || foreign != nil {
		t.Fatalf("unsupported queue = %+v, %v", foreign, err)
	}
	var version int
	if err := s.database.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("unsupported storage modified: %d, %v", version, err)
	}
}
