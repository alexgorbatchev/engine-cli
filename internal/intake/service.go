package intake

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

var errIntakeClosed = errors.New("intake owner is closed")

// Service owns an opened library's queue and preparation goroutine. Shutdown
// cancels and joins preparation before closing storage; replacement owners must
// complete Close before publishing a replacement library or queue database.
type Service struct {
	mu        sync.RWMutex
	store     *Store
	cancel    context.CancelFunc
	done      chan struct{}
	ready     chan struct{}
	wake      chan struct{}
	closed    bool
	closeErr  error
	statusMu  sync.RWMutex
	errorCode string
	pending   *preparationRound
}

type preparationRound struct {
	done chan struct{}
	err  error
}

// OpenService resumes durable preparation, but never imports into a collection.
// The parent context belongs to the API/library lifecycle, not an HTTP request.
func OpenService(parent context.Context, library string, quiet time.Duration) (*Service, error) {
	store, err := Open(parent, library)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	s := &Service{store: store, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), wake: make(chan struct{}, 1)}
	go s.run(ctx, quiet)
	s.Wake()
	return s, nil
}

// Ready waits for the initial durable receipt reconciliation, not media
// preparation. Opening remains asynchronous because an import may open its
// service while already owning History, which reconciliation must also read.
func (s *Service) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.done:
		return &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	case <-s.ready:
		return nil
	}
}

// Use keeps storage alive through a synchronous queue operation. It must not
// call Close or retain the Store beyond its callback. Mutations that enqueue or
// retry work call Wake after their durable state change succeeds.
func (s *Service) Use(ctx context.Context, run func(*Store) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return run(s.store)
}

func (s *Service) Wake() {
	s.requestPreparation()
}

func (s *Service) requestPreparation() *preparationRound {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.pending == nil {
		s.pending = &preparationRound{done: make(chan struct{})}
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return s.pending
}

// Process waits for a preparation round requested after the caller's durable
// enqueue/retry, completing when the worker reaches no eligible work. It never
// imports. A suspended queue completes without work; failures remain on items.
func (s *Service) Process(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	round := s.requestPreparation()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	case <-round.done:
		return round.err
	}
}

// ErrorCode reports only a completed worker failure, never raw diagnostics.
func (s *Service) ErrorCode() string {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.errorCode
}

func (s *Service) run(ctx context.Context, quiet time.Duration) {
	defer close(s.done)
	initial := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
		s.statusMu.Lock()
		round := s.pending
		s.pending = nil
		s.statusMu.Unlock()
		err := s.store.ReconcileImports(ctx)
		if ctx.Err() != nil {
			return
		}
		if initial {
			s.recordError(err)
			close(s.ready)
			initial = false
		}
		for err == nil {
			var item *Item
			item, err = s.store.PrepareNext(ctx, quiet)
			if item == nil {
				break
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.recordError(err)
		if round != nil {
			round.err = err
			close(round.done)
		}
	}
}

func (s *Service) recordError(err error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.errorCode = ""
	if err != nil {
		s.errorCode = progress.ErrorCode(err)
	}
}

func (s *Service) Close() error {
	// Cancellation precedes taking the exclusive mutex: an operation may be
	// awaiting preparation, and joining a worker must not hold its queue lock.
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.closeErr = s.store.Close()
	}
	return s.closeErr
}
