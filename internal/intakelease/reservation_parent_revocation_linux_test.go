package intakelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReservationParentDisappearsAfterResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	base := t.TempDir()
	first := filepath.Join(base, "a")
	parent := filepath.Join(base, "z")
	last := filepath.Join(parent, "library")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	anchors := filepath.Join(base, reservationDirectory)
	if err := os.Mkdir(anchors, 0700); err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(anchors, "a")
	if err := os.WriteFile(anchor, nil, 0600); err != nil {
		t.Fatal(err)
	}
	barrier := holdKernelLease(t, ctx, anchor)
	result := make(chan error, 1)
	go func() {
		lease, err := Replace(ctx, first, last)
		if lease != nil {
			t.Error("disappeared parent acquired ownership")
			err = errors.Join(lease.Close(), err)
		}
		result <- err
	}()
	// All names are resolved before the first anchor is opened. Revoke the
	// later parent while the kernel proves that the first open is blocked.
	barrier.waitBlocked(t)
	retained := parent + "-retained"
	if err := os.Rename(parent, retained); err != nil {
		t.Fatal(err)
	}
	barrier.release(t)
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("native parent disappearance lost: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refusal recreated removed parent: %v", err)
	}
	if entries, err := os.ReadDir(retained); err != nil || len(entries) != 0 {
		t.Fatalf("refusal enrolled retained parent: %v %v", entries, err)
	}
	// Failure on the second path must release the earlier lock, and restoring
	// the original parent must permit a complete retry.
	available, err := Replace(ctx, first)
	if err != nil {
		t.Fatalf("later parent failure stranded earlier owner: %v", err)
	}
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retained, parent); err != nil {
		t.Fatal(err)
	}
	recovered, err := Replace(ctx, first, last)
	if err != nil {
		t.Fatalf("restored parent refused ownership: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
}
