package transfer

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func readCompanions(ctx context.Context, c *collection) error {
	if err := readTags(ctx, c); err != nil {
		return err
	}
	return readColors(ctx, c)
}

// Check both independent snapshots before committing Engine. They cannot share
// its WAL transaction; any subsequent publication failure requires recovery.
func checkCompanions(ctx context.Context, c *collection) error {
	checks := []struct {
		code, expected string
		read           func() (string, error)
	}{
		{"tag_revision_stale", c.Tags.Revision(), func() (string, error) {
			state, err := tags.Read(ctx, c.Path)
			return state.Revision(), err
		}},
		{"color_revision_stale", c.Colors.Revision(), func() (string, error) {
			state, err := colors.Read(ctx, c.Path)
			return state.Revision(), err
		}},
	}
	for _, check := range checks {
		revision, err := check.read()
		if err != nil {
			return err
		}
		if revision != check.expected {
			return &progress.Error{Code: check.code, Cause: fmt.Errorf("companion state changed while applying the sync plan")}
		}
	}
	return nil
}

func (p *plan) publishCompanions(ctx context.Context) error {
	destination := p.destination
	publications := []func() error{
		func() error {
			next := p.plannedTags()
			if next.Revision() == destination.Tags.Revision() {
				return nil
			}
			_, err := tags.Replace(ctx, destination.Path, destination.Tags.Revision(), next)
			return err
		},
		func() error {
			_, err := colors.Replace(ctx, destination.Path, destination.Colors.Revision(), p.plannedColors())
			return err
		},
	}
	for _, publish := range publications {
		if err := publish(); err != nil {
			return errors.Join(err, history.RequireRecovery(ctx))
		}
	}
	return nil
}
