package history

import (
	"context"
	"errors"
	"sync"
	"testing"

	"zombiezen.com/go/sqlite"
)

func TestSmartlistReadCancellationRetainsOtherReaderLease(t *testing.T) {
	lib, _, _ := fixture(t)
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	// Enroll the actual native publisher lock before starting read leases.
	publisher, err := lock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	firstEntered := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := ReadSmartlistSafe(firstCtx, lib, func(ctx context.Context) (bool, error) {
			close(firstEntered)
			<-ctx.Done()
			return false, ctx.Err()
		})
		firstDone <- err
	}()
	select {
	case <-firstEntered:
	case err := <-firstDone:
		t.Fatalf("first reader did not acquire lease: %v", err)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	secondEntered := make(chan struct{})
	releaseSecond := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSecond) }) }
	defer release()
	secondDone := make(chan error, 1)
	go func() {
		_, err := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (bool, error) {
			close(secondEntered)
			select {
			case <-releaseSecond:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		})
		secondDone <- err
	}()
	select {
	case <-secondEntered:
	case err := <-secondDone:
		t.Fatalf("second reader did not share lease: %v", err)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first reader cancellation lost: %v", err)
	}
	publisher, err = lock(t.Context(), dir)
	if err == nil {
		_ = publisher.Close()
		t.Fatal("publisher entered while second reader still held its lease")
	}
	if sqlite.ErrCode(err) != sqlite.ResultBusy {
		t.Fatalf("publisher failed for a reason other than native exclusion: %v", err)
	}
	release()
	if err := <-secondDone; err != nil {
		t.Fatalf("first reader cancellation poisoned remaining lease: %v", err)
	}
	publisher, err = lock(t.Context(), dir)
	if err != nil {
		t.Fatalf("last reader did not release publisher exclusion: %v", err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
}
