package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestObserveEntriesWaitsWithoutChangingFailFastList(t *testing.T) {
	lib, release, finished := heldObservationHistory(t)
	if _, err := List(lib); err == nil {
		t.Fatal("List stopped refusing a concurrent publisher")
	}
	observed := make(chan error, 1)
	go func() {
		observed <- ObserveEntries(t.Context(), lib, func(_ context.Context, entries []Entry) error {
			if len(entries) != 1 || entries[0].Operation != "test.observation" || entries[0].State != "complete" {
				return errors.New("observer saw an incomplete publisher")
			}
			return nil
		})
	}()
	select {
	case err := <-observed:
		t.Fatalf("observation passed a held publisher: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
}

func TestObserveEntriesCancellationAndCallbackRelease(t *testing.T) {
	lib, release, finished := heldObservationHistory(t)
	ctx, cancel := context.WithCancel(t.Context())
	observed := make(chan error, 1)
	go func() {
		observed <- ObserveEntries(ctx, lib, func(context.Context, []Entry) error {
			return errors.New("cancelled observation reached callback")
		})
	}()
	select {
	case err := <-observed:
		t.Fatalf("observer did not wait for native ownership: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	if err := <-observed; !errors.Is(err, context.Canceled) {
		t.Fatalf("native wait lost cancellation: %v", err)
	}
	release()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	cause := errors.New("queue update refused")
	err := ObserveEntries(t.Context(), lib, func(ctx context.Context, _ []Entry) error {
		if _, err := Run(ctx, lib, "test.conflict", ScopeFiles, nil, func(context.Context) error { return nil }); err == nil {
			t.Error("callback did not retain History ownership")
		}
		return cause
	})
	if !errors.Is(err, cause) {
		t.Fatalf("callback error lost: %v", err)
	}
	if _, err := Run(t.Context(), lib, "test.after", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("observation leaked ownership: %v", err)
	}
	err = ObserveEntries(t.Context(), lib, func(context.Context, []Entry) error { return os.ErrNotExist })
	if !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrHistoryAbsent) {
		t.Fatalf("callback error was classified as absent History: %v", err)
	}
}

func TestObserveEntriesRefusesMissingAndPendingHistory(t *testing.T) {
	lib, _, _ := fixture(t)
	called := false
	err := ObserveEntries(t.Context(), lib, func(context.Context, []Entry) error { called = true; return nil })
	if !errors.Is(err, os.ErrNotExist) || !errors.Is(err, ErrHistoryAbsent) || called {
		t.Fatalf("missing History was observed: %v called=%t", err, called)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing History observation enrolled storage: %v", err)
	}
	if _, err := Run(t.Context(), lib, "test.before", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(lib, ".engine-cli", "smartlist-repair", "pending.json")
	if err := os.MkdirAll(filepath.Dir(pending), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, []byte("malformed pending intent"), 0600); err != nil {
		t.Fatal(err)
	}
	err = ObserveEntries(t.Context(), lib, func(context.Context, []Entry) error { called = true; return nil })
	if progress.ErrorCode(err) != "smartlist_repair_pending" || called {
		t.Fatalf("pending repair reached observer: %v called=%t", err, called)
	}
}

func TestObserveEntriesRefusalPreservesEvidenceAndReleasesOwner(t *testing.T) {
	lib, _, _ := fixture(t)
	called := false
	observe := func(context.Context, []Entry) error { called = true; return nil }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ObserveEntries(ctx, lib, observe); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled observation reached callback: %v called=%t", err, called)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled observation enrolled storage: %v", err)
	}
	if _, err := Run(t.Context(), lib, "test.before", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, ".engine-cli", "history", "damaged.json")
	evidence := []byte("malformed history entry")
	if err := os.WriteFile(path, evidence, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ObserveEntries(t.Context(), lib, observe); err == nil || errors.Is(err, ErrHistoryAbsent) || called {
		t.Fatalf("damaged entry reached observation: %v called=%t", err, called)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(evidence) {
		t.Fatalf("observation changed damaged evidence: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := ObserveEntries(t.Context(), lib, observe); err != nil || !called {
		t.Fatalf("refusal leaked ownership: %v called=%t", err, called)
	}
	if _, err := Run(t.Context(), lib, "test.after", ScopeFiles, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("observer did not release native owner: %v", err)
	}
}

func heldObservationHistory(t *testing.T) (string, func(), <-chan error) {
	t.Helper()
	lib, _, _ := fixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	entered := make(chan struct{})
	continueOwner := make(chan struct{})
	finished := make(chan error, 1)
	done := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(continueOwner) }) }
	t.Cleanup(func() {
		release()
		cancel()
		<-done
	})
	go func() {
		defer close(done)
		_, err := Run(ctx, lib, "test.observation", ScopeFiles, nil, func(context.Context) error {
			close(entered)
			select {
			case <-continueOwner:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		finished <- err
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("publisher failed before callback: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return lib, release, finished
}
