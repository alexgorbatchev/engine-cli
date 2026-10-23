package api

import (
	"context"
	"encoding/json"

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
	op := define("track.suggest", false, []string{"harness"}, func(ctx context.Context, p suggestionParams) (any, error) {
		return suggest.Run(ctx, s.config.LibraryDir, p.Selection, p.AskOptions, s.toolClient(), nil)
	})
	op.reduceResult = accumulateSuggestions
	return op
}

func accumulateSuggestions(current any, data json.RawMessage) any {
	var batch suggest.Report
	if err := json.Unmarshal(data, &batch); err != nil {
		return current
	}
	if previous, ok := current.(*suggest.Report); ok && previous != nil {
		// Published snapshots stay immutable while polling encodes them outside
		// the job lock. SSE carries only the new batch, avoiding repeated data.
		batch.Items = append(append([]suggest.Item{}, previous.Items...), batch.Items...)
	}
	return &batch
}
