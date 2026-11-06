package intake

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func (s *Store) change(ctx context.Context, run func(context.Context) (*Item, error)) (item *Item, err error) {
	err = intakestate.With(ctx, s.library, func(ctx context.Context) error {
		item, err = run(ctx)
		return err
	})
	return item, err
}

func (s *Store) setControl(ctx context.Context, reason string) error {
	return intakestate.With(ctx, s.library, func(ctx context.Context) error {
		return intakecontrol.Set(ctx, s.database, reason)
	})
}
