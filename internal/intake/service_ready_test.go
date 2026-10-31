package intake

import (
	"context"
	"errors"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestServiceReadyRefusesCompletedCancellationAndClosure(t *testing.T) {
	service, err := OpenService(t.Context(), t.TempDir(), 0)
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
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	t.Run("cancelled observation", func(t *testing.T) {
		for range 100 {
			if err := service.Ready(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("completed cancellation reported ready: %v", err)
			}
		}
	})
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("closed owner", func(t *testing.T) {
		for range 100 {
			if err := service.Ready(t.Context()); progress.ErrorCode(err) != "intake_closed" {
				t.Fatalf("closed owner reported ready: %v", err)
			}
		}
	})
}
