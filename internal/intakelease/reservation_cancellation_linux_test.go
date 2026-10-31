package intakelease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReservationCancellationDuringNativeAcquisitionReleasesPaths(t *testing.T) {
	for _, morePaths := range []bool{false, true} {
		t.Run(fmt.Sprintf("more-paths=%t", morePaths), func(t *testing.T) {
			guard, stop := context.WithTimeout(t.Context(), 15*time.Second)
			defer stop()
			base := t.TempDir()
			paths := []string{filepath.Join(base, "a")}
			if morePaths {
				paths = append(paths, filepath.Join(base, "b"))
			}
			anchors := filepath.Join(base, reservationDirectory)
			if err := os.Mkdir(anchors, 0700); err != nil {
				t.Fatal(err)
			}
			anchor := filepath.Join(anchors, "a")
			if err := os.WriteFile(anchor, nil, 0600); err != nil {
				t.Fatal(err)
			}
			barrier := holdKernelLease(t, guard, anchor)
			ctx, cancel := context.WithCancel(guard)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				lease, err := Replace(ctx, paths...)
				if lease != nil {
					err = errors.Join(errors.New("cancelled acquisition returned ownership"), err, lease.Close())
				}
				result <- err
			}()
			barrier.waitBlocked(t)
			cancel()
			barrier.release(t)
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("late cancellation was lost: %v", err)
				}
			case <-guard.Done():
				t.Fatal("native acquisition did not join after kernel lease release")
			}
			recovered, err := Replace(guard, paths...)
			if err != nil {
				t.Fatalf("cancelled native acquisition leaked ownership: %v", err)
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
