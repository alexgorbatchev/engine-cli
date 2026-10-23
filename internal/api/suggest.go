package api

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/suggest"
)

type suggestionParams suggest.Selection

func (p suggestionParams) validate() error { return suggest.Selection(p).Validate() }

func (s *Server) suggestionOperation() operation {
	return define("track.suggest", false, nil, func(ctx context.Context, p suggestionParams) (any, error) {
		return suggest.Run(ctx, s.config.LibraryDir, suggest.Selection(p), s.config.LLM)
	})
}
