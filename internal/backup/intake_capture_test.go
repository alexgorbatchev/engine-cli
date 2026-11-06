package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestCompanionCaptureWaitsBeforePresenceEnumeration(t *testing.T) {
	library := t.TempDir()
	queue, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	source := filepath.Join(t.TempDir(), "original.wav")
	if err := os.WriteFile(source, []byte("source retained"), 0600); err != nil {
		t.Fatal(err)
	}
	item, err := queue.Enqueue(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	err = intakestate.With(t.Context(), library, func(context.Context) error {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if _, _, err := snapshotCompanion(ctx, library, staging); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("companion capture crossed transition owner: %v", err)
		}
		if _, err := os.Stat(filepath.Join(staging, companionFolder)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("capture published companion folder before acquiring owner: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, _, err := snapshotCompanion(t.Context(), library, staging); err != nil || count != 1 {
		t.Fatalf("capture after owner release: count=%d err=%v", count, err)
	}
	if _, err := queue.Cancel(t.Context(), item.ID); err != nil {
		t.Fatal("capture leaked native owner:", err)
	}
	image, err := db.Open(filepath.Join(staging, companionFolder, companion.IntakeName), true)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	var status string
	if err := image.QueryRow("SELECT status FROM Item WHERE id=?", item.ID).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("captured queue changed with live transition: %q %v", status, err)
	}
}
