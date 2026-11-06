package intake

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestServiceCloseJoinsAndRestartResumes(t *testing.T) {
	queue, lib := queueFixture(t)
	source := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(source, []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	item, err := queue.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	service, err := OpenService(t.Context(), lib, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	waitQueueStatus(t, queue, item.ID, "preparing")
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	err = service.Use(t.Context(), func(*Store) error { called = true; return nil })
	if called || progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("closed service accepted operation: called=%v, %v", called, err)
	}
	items, err := queue.List(t.Context())
	if err != nil || len(items) != 1 || items[0].Status != "preparing" || items[0].Attempt != 1 {
		t.Fatalf("interrupted preparation lost: %+v, %v", items, err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	// Renaming the actual queue after shutdown exercises native handle release;
	// restart must observe the same durable item, not an unlinked open database.
	path := filepath.Join(lib, ".engine-cli", companion.IntakeName)
	if err := os.Rename(path, path+".retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".retained", path); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenService(t.Context(), lib, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	if err := restarted.Use(t.Context(), func(s *Store) error {
		waitQueueStatus(t, s, item.ID, "staged")
		items, err := s.List(t.Context())
		if err != nil || len(items) != 1 || items[0].Attempt != 2 || items[0].ContentHash == "" {
			t.Fatalf("restart duplicated or failed preparation: %+v, %v", items, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "retained source" {
		t.Fatalf("lifecycle changed original: %q, %v", data, err)
	}
}

func waitQueueStatus(t *testing.T, queue *Store, id int64, status string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := queue.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == id && item.Status == status {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("queue did not reach %s: %+v", status, items)
		case <-ticker.C:
		}
	}
}
