package api

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dj/engine-cli/internal/duplicate"
	"github.com/dj/engine-cli/internal/progress"
)

type duplicateListParams struct {
	Type string `json:"type"`
}
type duplicateInspectParams struct {
	IDs []int64 `json:"ids"`
}

func (p duplicateInspectParams) validate() error {
	if len(p.IDs) == 0 {
		return fmt.Errorf("ids must not be empty")
	}
	for _, id := range p.IDs {
		if id <= 0 {
			return fmt.Errorf("ids must contain positive track IDs")
		}
	}
	return nil
}

func (s *Server) duplicateOperations() []operation {
	return []operation{
		define("track.duplicate.list", false, nil, func(ctx context.Context, p duplicateListParams) (any, error) {
			if p.Type == "" {
				p.Type = "all"
			}
			return s.database(ctx, func(d *sql.DB) (any, error) { return duplicate.List(ctx, d, p.Type) })
		}),
		define("track.duplicate.inspect", false, []string{"ids"}, func(ctx context.Context, p duplicateInspectParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				result := make([]*duplicate.Track, 0, len(p.IDs))
				for i, id := range p.IDs {
					track, err := duplicate.Inspect(ctx, d, id)
					if err != nil {
						return result, err
					}
					result = append(result, track)
					progress.Item(ctx, "track.duplicate.inspect", i+1, len(p.IDs))
				}
				return result, nil
			})
		}),
	}
}
