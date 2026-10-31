package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const intakeQuietPeriod = 2 * time.Second

// intakeLibraries is shared by all opened-library servers. Replacement obtains
// its per-library owner before the API scheduler or History owner, so joining
// an import cannot wait on a lock held by its replacement.
type intakeLibraries struct {
	mu      sync.Mutex
	entries map[string]*intakeLibrary
	closed  bool
	quiet   time.Duration
}

var errIntakeClosed = errors.New("intake owner is closed")

type intakeLibrary struct {
	service *intake.Service
	blocked bool
	owner   chan struct{}
}

func newIntakeLibraries() *intakeLibraries {
	return &intakeLibraries{entries: make(map[string]*intakeLibrary), quiet: intakeQuietPeriod}
}

func (q *intakeLibraries) entry(path string) *intakeLibrary {
	e := q.entries[path]
	if e == nil {
		e = &intakeLibrary{owner: make(chan struct{}, 1)}
		q.entries[path] = e
	}
	return e
}

// open resumes only already enrolled queues unless an explicit queue action
// requests creation. Merely browsing a library must not enroll intake storage.
func (q *intakeLibraries) open(path string, create bool) (*intake.Service, error) {
	path, err := intakeLibraryPath(path)
	if err != nil {
		return nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	}
	e := q.entry(path)
	if e.blocked {
		return nil, &progress.Error{Code: "intake_replacing", Cause: errors.New("library replacement owns intake")}
	}
	if e.service != nil {
		return e.service, nil
	}
	if !create {
		present, err := companion.Database(path, companion.IntakeName)
		if err != nil {
			return nil, &progress.Error{Code: "intake_recovery_required", Cause: err}
		}
		if present == "" {
			return nil, nil
		}
	}
	service, err := intake.OpenService(context.Background(), path, q.quiet)
	if err != nil {
		return nil, &progress.Error{Code: "intake_recovery_required", Cause: err}
	}
	e.service = service
	return service, nil
}

func (q *intakeLibraries) wake(path string) {
	path, err := intakeLibraryPath(path)
	if err != nil {
		return // A moved/deleted library has no remaining worker to notify.
	}
	q.mu.Lock()
	e := q.entries[path]
	if e != nil && e.service != nil {
		e.service.Wake()
	}
	q.mu.Unlock()
}

// stop detaches the service before joining it. New uses refuse immediately;
// they never queue behind a replacement while holding collection ownership.
// The returned release must run after collection/History ownership is released.
func (q *intakeLibraries) stop(ctx context.Context, path string) (func(bool) error, error) {
	path, err := intakeLibraryPath(path)
	if err != nil {
		return nil, err
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil, &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	}
	e := q.entry(path)
	q.mu.Unlock()
	select {
	case e.owner <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		<-e.owner
		return nil, &progress.Error{Code: "intake_closed", Cause: errIntakeClosed}
	}
	e.blocked = true
	service := e.service
	e.service = nil
	q.mu.Unlock()
	if service != nil {
		err = service.Close()
	}
	release := func(reopen bool) error {
		q.mu.Lock()
		e.blocked = false
		closed := q.closed
		q.mu.Unlock()
		defer func() { <-e.owner }()
		if closed || !reopen {
			return nil
		}
		// Replacement has already committed. A failed reopen is queue recovery
		// state, not failure of the committed collection job.
		_, _ = q.open(path, false) // intake.list retries and reports recovery state.
		return nil
	}
	if err != nil {
		return nil, errors.Join(err, release(true))
	}
	return release, nil
}

func intakeLibraryPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func (s *Server) replacementIntake(ctx context.Context, op operation, bound *boundOperation) (func() error, error) {
	if bound.preview {
		return nil, nil
	}
	path := bound.library
	switch op.Name {
	case "library.restore", "library.undo", "library.delete", "library.move", "intake.pause", "intake.resume":
	case "library.sync":
		path = bound.replacementLibrary
	default:
		return nil, nil
	}
	if path == "" {
		path = s.config.LibraryDir
	}
	canonical, err := filepath.Abs(path)
	if err == nil {
		canonical, err = filepath.EvalSymlinks(canonical)
	}
	if err != nil {
		return nil, err
	}
	paths := []string{canonical}
	if op.Name == "library.move" {
		destination, err := libraryops.MoveDestination(canonical, bound.replacementLibrary)
		if err != nil {
			return nil, err
		}
		paths = append(paths, destination)
	}
	resume, err := s.intake.stop(ctx, canonical)
	if err != nil {
		return nil, err
	}
	// Control changes reopen themselves. Undo selects and reserves both relocation
	// paths in History before acquiring its exclusive mutation owner.
	if op.Name == "intake.pause" || op.Name == "intake.resume" || op.Name == "library.undo" {
		return func() error { return resume(true) }, nil
	}
	lease, err := intakelease.Replace(ctx, paths...)
	if err != nil {
		return nil, errors.Join(err, resume(true))
	}
	return func() error {
		// Release cross-process exclusion before reopening the local worker.
		closeErr := lease.Close()
		return errors.Join(closeErr, resume(closeErr == nil))
	}, nil
}

// Relocation can change the canonical library directory. Resume only a native
// successful result, after the old queue and collection owners are released.
func (s *Server) resumeRelocatedIntake(operation string, result any) {
	var path string
	if operation == "library.move" {
		if plan, ok := result.(*libraryops.Plan); ok && plan != nil {
			path = plan.Destination
		}
	}
	if operation == "library.undo" {
		if entry, ok := result.(*history.Entry); ok && entry != nil && entry.RelocationFrom != "" {
			path = entry.Library
		}
	}
	if path != "" {
		// The collection result is already committed. Queue recovery remains
		// available through intake.list without replacing that result.
		_, _ = s.intake.open(path, false)
	}
}

func (q *intakeLibraries) close() error {
	q.mu.Lock()
	q.closed = true
	var services []*intake.Service
	for _, e := range q.entries {
		if e.service != nil {
			services = append(services, e.service)
			e.service = nil
		}
	}
	q.mu.Unlock()
	var err error
	for _, service := range services {
		err = errors.Join(err, service.Close())
	}
	return err
}
