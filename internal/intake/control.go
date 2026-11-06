package intake

import (
	"context"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func (s *Store) Control(ctx context.Context) (intakecontrol.State, error) {
	return intakecontrol.Read(ctx, s.database)
}

// Pause waits for the current native preparation owner before returning.
// Services cancel and join preparation first so quiet-time waits do not delay
// an interactive pause or a storage replacement.
func (s *Store) Pause(ctx context.Context) error {
	return s.withWorker(ctx, func(ctx context.Context) error {
		return intakecontrol.Set(ctx, s.database, "manual")
	})
}

// Resume is explicit authorization to process this library's saved source paths.
// Missing import History cannot be acknowledged away: retained receipts must
// remain quarantined until their actual collection ownership is recovered.
func (s *Store) Resume(ctx context.Context) error {
	return s.withWorker(ctx, func(ctx context.Context) error {
		if err := s.reconcileImports(ctx); err != nil {
			return err
		}
		return intakecontrol.Set(ctx, s.database, "")
	})
}

func (s *Store) requireActive(ctx context.Context) error {
	state, err := s.Control(ctx)
	if err != nil {
		return err
	}
	if state.Paused {
		return &progress.Error{Code: "intake_paused", Cause: errors.New("intake preparation and import are paused")}
	}
	return nil
}
