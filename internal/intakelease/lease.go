// Package intakelease excludes library replacement while intake storage is open.
// Its persistent coordination entries remain outside moved library trees.
package intakelease

import (
	"context"
	"errors"
)

// Lease retains native ownership until Close, including after cancellation.
// The caller must join users of queue storage before releasing its observation.
type Lease struct {
	reservation *reservation
}

// Observe allows independent queue owners while refusing an active replacement.
func Observe(ctx context.Context, library string) (*Lease, error) {
	return acquire(ctx, false, library)
}

// Replace excludes every queue owner at each path, including vacant move
// destinations. Acquire it before collection/History ownership, after stopping
// local workers, and retain it through replacement and rollback completion.
func Replace(ctx context.Context, libraries ...string) (*Lease, error) {
	return acquire(ctx, true, libraries...)
}

func acquire(ctx context.Context, exclusive bool, libraries ...string) (*Lease, error) {
	if len(libraries) == 0 {
		return nil, errors.New("intake path ownership requires a library")
	}
	for _, path := range libraries {
		if path == "" {
			return nil, errors.New("intake path ownership requires a nonempty library path")
		}
	}
	r, err := reservePaths(ctx, exclusive, libraries...)
	if err != nil {
		return nil, err
	}
	return &Lease{reservation: r}, nil
}

func (l *Lease) Close() error {
	if l.reservation == nil {
		return nil
	}
	return l.reservation.Close()
}
