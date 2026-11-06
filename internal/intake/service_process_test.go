package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestServiceProcessDrainsWithoutImporting(t *testing.T) {
	queue, lib := queueFixture(t)
	service, err := OpenService(t.Context(), lib, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	for round := range 2 {
		source := filepath.Join(t.TempDir(), "incoming.wav")
		if err := os.WriteFile(source, []byte("retained original"), 0600); err != nil {
			t.Fatal(err)
		}
		item, err := queue.Enqueue(t.Context(), source)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Process(t.Context()); err != nil {
			t.Fatal(err)
		}
		items, err := queue.List(t.Context())
		if err != nil || len(items) != round+1 || items[round].ID != item.ID || items[round].Status != "staged" || items[round].HistoryID != "" || items[round].ContentHash == "" {
			t.Fatalf("process returned before durable staging: %+v %v", items, err)
		}
		if data, err := os.ReadFile(source); err != nil || string(data) != "retained original" {
			t.Fatalf("source changed: %q %v", data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("preparation created collection history: %v", err)
	}
}

func TestServiceProcessCancellationAndClose(t *testing.T) {
	queue, lib := queueFixture(t)
	source := filepath.Join(t.TempDir(), "incoming.wav")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
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
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- service.Process(ctx) }()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("processing ignored caller cancellation: %v", err)
	}
	go func() { result <- service.Process(t.Context()) }()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("shutdown did not settle waiting process: %v", err)
	}
}
