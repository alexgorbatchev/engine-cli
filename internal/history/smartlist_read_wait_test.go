package history

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func heldReceiptObservation(t *testing.T) (string, func()) {
	t.Helper()
	lib := t.TempDir()
	if _, err := Run(t.Context(), lib, "test.receipt", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	entered, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ObserveEntries(ctx, lib, func(ctx context.Context, _ []Entry) error {
			close(entered)
			select {
			case <-finish:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	t.Cleanup(func() {
		release()
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("receipt observation did not acquire History:", ctx.Err())
	}
	return lib, release
}

func TestSmartlistReadWaitsForReceiptObservation(t *testing.T) {
	lib, release := heldReceiptObservation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		value, err := ReadSmartlistSafe(ctx, lib, func(context.Context) (string, error) { return "observed", nil })
		if err == nil && value != "observed" {
			err = errors.New("read lost its result")
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("read did not wait for receipt reconciliation: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := List(lib); err == nil {
		t.Fatal("History List no longer refuses concurrent ownership")
	}
	release()
	if err := <-done; err != nil {
		t.Fatal("read failed after reconciliation released History:", err)
	}
}

func TestSmartlistReadWaitCancellationDoesNotBlockAnotherLibrary(t *testing.T) {
	lib, _ := heldReceiptObservation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ReadSmartlistSafe(ctx, lib, func(context.Context) (bool, error) {
			return false, errors.New("cancelled reader entered callback")
		})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("read did not wait for native ownership: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	other, release := heldReceiptObservation(t)
	release()
	otherCtx, cancelOther := context.WithTimeout(t.Context(), time.Second)
	defer cancelOther()
	if _, err := ReadSmartlistSafe(otherCtx, other, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal("waiting reader blocked unrelated library:", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("reader wait lost cancellation:", err)
	}
}

func TestSmartlistReadersShareLeaseAfterNativeWait(t *testing.T) {
	lib, release := heldReceiptObservation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	const readers = 8
	entered := make(chan struct{}, readers)
	done := make(chan error, readers)
	finish := make(chan struct{})
	var once sync.Once
	releaseReaders := func() { once.Do(func() { close(finish) }) }
	defer releaseReaders()
	for range readers {
		go func() {
			_, err := ReadSmartlistSafe(ctx, lib, func(ctx context.Context) (bool, error) {
				entered <- struct{}{}
				select {
				case <-finish:
					return true, nil
				case <-ctx.Done():
					return false, ctx.Err()
				}
			})
			done <- err
		}()
	}
	release()
	for range readers {
		select {
		case <-entered:
		case err := <-done:
			t.Fatal("reader did not join shared lease:", err)
		case <-ctx.Done():
			t.Fatal("native wait serialized overlapping read callbacks:", ctx.Err())
		}
	}
	releaseReaders()
	for range readers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSmartlistWaitingReaderCancellationRetainsOtherWaiters(t *testing.T) {
	for _, cancelled := range []int{0, 1} {
		t.Run(map[int]string{0: "acquirer", 1: "follower"}[cancelled], func(t *testing.T) {
			lib, release := heldReceiptObservation(t)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			defer func() { cancel(); wg.Wait() }()
			var cancels [2]context.CancelFunc
			var done [2]chan error
			called := make(chan int, 2)
			for i := range done {
				readerCtx, cancelReader := context.WithCancel(ctx)
				cancels[i] = cancelReader
				defer cancelReader()
				done[i] = make(chan error, 1)
				wg.Go(func() {
					_, err := ReadSmartlistSafe(readerCtx, lib, func(context.Context) (bool, error) {
						called <- i
						return true, nil
					})
					done[i] <- err
				})
				// The first reader must publish its native wait before the second
				// arrives; exercise cancellation of both roles independently.
				if i == 0 {
					waitSmartlistReadAcquisition(t, ctx, lib)
				}
			}
			cancels[cancelled]()
			if err := <-done[cancelled]; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled waiter: %v", err)
			}
			release()
			remaining := 1 - cancelled
			if err := <-done[remaining]; err != nil {
				t.Fatalf("one cancellation poisoned another waiter: %v", err)
			}
			if got := <-called; got != remaining {
				t.Fatalf("cancelled waiter entered callback: %d", got)
			}
		})
	}
}

func waitSmartlistReadAcquisition(t *testing.T, ctx context.Context, lib string) {
	t.Helper()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		smartlistReaders.Lock()
		owner := smartlistReaders.owners[dir]
		pending := owner != nil && owner.conn == nil
		smartlistReaders.Unlock()
		if pending {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("reader did not wait for native History ownership:", ctx.Err())
		case <-tick.C:
		}
	}
}
