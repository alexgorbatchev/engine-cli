package intake

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func TestCancelledQueueAccessPreservesStorageAndDoesNotEnrollWatch(t *testing.T) {
	s, library := queueFixture(t)
	source := sourceFixture(t, "cancelled.wav")
	path := filepath.Join(library, ".engine-cli", companion.IntakeName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if item, err := s.Enqueue(ctx, source); !errors.Is(err, context.Canceled) || item != nil {
		t.Fatalf("cancelled enqueue accepted source: %+v %v", item, err)
	}
	if err := ValidateDatabase(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replacement validation accepted image: %v", err)
	}
	if scan, directories, err := s.reconcileWatch(ctx, 0); !errors.Is(err, context.Canceled) || scan.Completed || len(directories) != 0 {
		t.Fatalf("cancelled discovery accepted or registered roots: %+v %+v %v", scan, directories, err)
	}
	if opened, err := openWatch(ctx, library); !errors.Is(err, context.Canceled) || opened != nil {
		if opened != nil {
			if err := opened.Close(); err != nil {
				t.Error(err)
			}
		}
		t.Fatalf("cancelled watch enrollment accepted: %v %v", opened, err)
	}
	if watch, err := companion.Database(library, companion.WatchName); err != nil || watch != "" {
		t.Fatalf("cancelled access enrolled watch storage: %q %v", watch, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("cancelled access changed queue image: %v", err)
	}
	items, err := s.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("cancelled source appeared in queue: %+v %v", items, err)
	}
	// A refused operation must not strand ownership needed by the next caller.
	if item, err := s.Enqueue(t.Context(), source); err != nil || item == nil || item.Status != "queued" {
		t.Fatalf("fresh request could not recover after cancellation: %+v %v", item, err)
	}
}
