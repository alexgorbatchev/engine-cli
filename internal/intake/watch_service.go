package intake

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/fsnotify/fsnotify"
	"zombiezen.com/go/sqlite"
)

const watchReconcileInterval = 30 * time.Second
const watchEventDelay = 100 * time.Millisecond

type watchRequest struct {
	ctx    context.Context
	change func(context.Context, *Store) (WatchState, error)
	done   chan watchResponse
}

type watchResponse struct {
	state WatchState
	scan  WatchScan
	err   error
}

type watchManager struct {
	requests chan watchRequest
	wake     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	active   context.CancelFunc
	pending  int
	phase    string
	code     string
	closeErr error
}

func newWatchManager() *watchManager {
	return &watchManager{requests: make(chan watchRequest), wake: make(chan struct{}, 1), done: make(chan struct{}), phase: "inactive"}
}

func (m *watchManager) registerChange() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending++
	if m.active != nil {
		m.active()
	}
}

func (m *watchManager) acceptChange() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending--
}

func (m *watchManager) startScan(cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = cancel
	if m.pending > 0 {
		cancel()
	}
}

func (m *watchManager) status(phase string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phase, m.code = phase, ""
	if err != nil {
		m.code = progress.ErrorCode(err)
	}
}

func (m *watchManager) decorate(state WatchState) WatchState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state.Manager, state.ErrorCode = m.phase, m.code
	return state
}

func (s *Service) WatchList(ctx context.Context) (state WatchState, err error) {
	err = s.Use(ctx, func(store *Store) error {
		state, err = store.WatchList(ctx)
		return err
	})
	return s.watch.decorate(state), err
}

func (s *Service) watchChange(ctx context.Context, change func(context.Context, *Store) (WatchState, error)) (WatchState, error) {
	response, err := s.watch.request(ctx, change)
	return response.state, err
}

func (s *Service) WatchAdd(ctx context.Context, opts WatchAddOptions) (WatchState, error) {
	return s.watchChange(ctx, func(ctx context.Context, store *Store) (WatchState, error) { return store.WatchAdd(ctx, opts) })
}

func (s *Service) WatchSetEnabled(ctx context.Context, opts WatchRootOptions, enabled bool) (WatchState, error) {
	return s.watchChange(ctx, func(ctx context.Context, store *Store) (WatchState, error) {
		return store.WatchSetEnabled(ctx, opts, enabled)
	})
}

func (s *Service) WatchRemove(ctx context.Context, opts WatchRootOptions) (WatchState, error) {
	return s.watchChange(ctx, func(ctx context.Context, store *Store) (WatchState, error) { return store.WatchRemove(ctx, opts) })
}

func (s *Service) WatchActivate(ctx context.Context, revision int64) (WatchState, error) {
	return s.watchChange(ctx, func(ctx context.Context, store *Store) (WatchState, error) { return store.WatchActivate(ctx, revision) })
}

func (s *Service) WatchReconcile(ctx context.Context) (WatchScan, error) {
	response, err := s.watch.request(ctx, nil)
	return response.scan, err
}

func (m *watchManager) request(ctx context.Context, change func(context.Context, *Store) (WatchState, error)) (watchResponse, error) {
	if err := ctx.Err(); err != nil {
		return watchResponse{}, err
	}
	request := watchRequest{ctx: ctx, change: change, done: make(chan watchResponse, 1)}
	if change != nil {
		// Retain the interruption until acceptance. A scan registered between
		// this registration and the send must not block command acceptance.
		m.registerChange()
	}
	select {
	case <-ctx.Done():
		if change != nil {
			m.acceptChange()
		}
		return watchResponse{}, ctx.Err()
	case <-m.done:
		if change != nil {
			m.acceptChange()
		}
		return watchResponse{}, watchFailure("intake_closed", errIntakeClosed)
	case m.requests <- request:
	}
	// Accepted mutations return their actual committed outcome. The owner uses
	// this request context while waiting for native locks and before committing.
	select {
	case response := <-request.done:
		return response, response.err
	case <-m.done:
		select {
		case response := <-request.done:
			return response, response.err
		default:
			return watchResponse{}, watchFailure("intake_closed", errIntakeClosed)
		}
	}
}

func (s *Service) runWatch(ctx context.Context, quiet time.Duration) {
	m := s.watch
	defer close(m.done)
	var owner *sqlite.Conn
	var events *watchEvents
	defer func() {
		if events != nil {
			m.closeErr = errors.Join(m.closeErr, events.close())
		}
		if owner != nil {
			m.closeErr = errors.Join(m.closeErr, owner.Close())
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		var request *watchRequest
		select {
		case <-ctx.Done():
			return
		case command := <-m.requests:
			request = &command
			if command.change != nil {
				m.acceptChange()
			}
		case <-m.wake:
		case <-timer.C:
		}
		if request != nil && request.change != nil {
			changeCtx, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(request.ctx, cancel)
			var state WatchState
			var err error
			if owner == nil {
				owner, err = s.store.openOwner(changeCtx, "watch-owner.db", "watch_owned")
			}
			if err == nil {
				state, err = request.change(changeCtx, s.store)
			}
			stop()
			cancel()
			request.done <- watchResponse{state: m.decorate(state), err: err}
			if err == nil {
				timer.Reset(0)
			}
			continue
		}
		// This full scan covers hints already queued. Hints arriving while it
		// traverses must remain queued for another pass.
		select {
		case <-m.wake:
		default:
		}
		scanCtx, cancel := context.WithCancel(ctx)
		stop := func() bool { return false }
		if request != nil {
			stop = context.AfterFunc(request.ctx, cancel)
		}
		m.startScan(cancel)
		result, directories, err := s.watchRound(scanCtx, quiet, &owner)
		stop()
		cancel()
		m.mu.Lock()
		m.active = nil
		m.mu.Unlock()
		changed := false
		if events != nil || err == nil && len(directories) > 0 {
			var eventErr error
			changed, eventErr = m.syncWatchDirectories(ctx, &events, directories)
			if eventErr != nil {
				err = errors.Join(err, watchFailure("watch_events_unavailable", eventErr))
			}
		}
		phase := "inactive"
		if result.Roots > 0 {
			phase = "running"
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			phase = "degraded"
		}
		m.status(phase, err)
		result.State = m.decorate(result.State)
		if request != nil {
			request.done <- watchResponse{scan: result, err: err}
		}
		if result.Queued+result.Associated > 0 {
			s.requestPreparation()
		}
		delay := watchReconcileInterval
		if result.Pending > 0 {
			delay = max(quiet, watchEventDelay)
		}
		if changed {
			// A descendant may gain files after traversal but before its watch
			// is installed. Repeat once with the expanded watch set covering it.
			delay = 0
		}
		timer.Reset(delay)
	}
}

func (s *Service) watchRound(ctx context.Context, quiet time.Duration, owner **sqlite.Conn) (WatchScan, []watchDirectory, error) {
	state, err := s.store.WatchList(ctx)
	if err != nil || len(state.Roots) == 0 {
		return WatchScan{Completed: err == nil, State: state}, nil, err
	}
	if *owner == nil {
		*owner, err = s.store.openOwner(ctx, "watch-owner.db", "watch_owned")
		if err != nil {
			return WatchScan{State: state}, nil, err
		}
	}
	return s.store.reconcileWatch(ctx, quiet)
}

func (m *watchManager) drainWatchEvents(ctx context.Context, watcher *fsnotify.Watcher, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-watcher.Events:
			if !ok {
				return
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			m.status("degraded", watchFailure("watch_events_unavailable", err))
		}
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}
