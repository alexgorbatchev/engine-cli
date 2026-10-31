package api

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/duplicate"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type duplicateListParams struct {
	Type string `json:"type"`
}
type duplicateScanParams struct {
	IDs  []int64  `json:"ids"`
	Refs []string `json:"refs"`
}
type duplicateInspectParams struct {
	IDs  []int64  `json:"ids"`
	Refs []string `json:"refs"`
}

func (p duplicateInspectParams) validate() error {
	if len(p.Refs) > 0 {
		if len(p.IDs) > 0 {
			return fmt.Errorf("choose refs or ids")
		}
		return details.ValidateRefs(p.Refs)
	}
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
		define("track.duplicate.scan", true, nil, func(ctx context.Context, p duplicateScanParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return duplicate.AudioGroups(ctx, d, s.config.LibraryDir, duplicate.AudioOptions{IDs: p.IDs, Refs: p.Refs, Generate: true})
			})
		}),
		defineCollectionRead("track.duplicate.list", nil, func(ctx context.Context, p duplicateListParams) (any, error) {
			if p.Type == "" {
				p.Type = "all"
			}
			return s.database(ctx, func(d *sql.DB) (any, error) {
				if p.Type == "audio" {
					return duplicate.AudioGroups(ctx, d, s.config.LibraryDir, duplicate.AudioOptions{})
				}
				return duplicate.List(ctx, d, p.Type)
			})
		}),
		defineCollectionRead("track.duplicate.inspect", nil, func(ctx context.Context, p duplicateInspectParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				if len(p.Refs) > 0 {
					ids, err := identity.ResolveBatch(ctx, d, s.config.LibraryDir, "track", p.Refs)
					if err != nil {
						return nil, err
					}
					p.IDs = ids
				}
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
