package api

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/suggest"
)

type suggestionParams struct {
	suggest.Selection
	acquire.AskOptions
}

func (p suggestionParams) validate() error {
	if err := p.Selection.Validate(); err != nil {
		return err
	}
	return p.AskOptions.Validate()
}

func (s *Server) suggestionOperation() operation {
	return define("track.suggest", false, []string{"harness"}, func(ctx context.Context, p suggestionParams) (any, error) {
		return suggest.Run(ctx, s.config.LibraryDir, p.Selection, p.AskOptions, s.toolClient())
	})
}
