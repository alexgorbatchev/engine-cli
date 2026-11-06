package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type capitalizationParams track.CapitalizationOptions

func (p capitalizationParams) validate() error        { return track.CapitalizationOptions(p).Validate() }
func (p capitalizationParams) operationPreview() bool { return p.DryRun }
func (p capitalizationParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

func (s *Server) capitalizationOperation() operation {
	before := func(ctx context.Context, p capitalizationParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) { return track.CheckCapitalization(ctx, d, track.CapitalizationOptions(p)) })
		return err
	}
	return defineChecked("track.batch.capitalize", "collection", []string{"identifiers", "fields", "recipe"}, before, func(ctx context.Context, p capitalizationParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				plan, err := track.PreviewCapitalization(ctx, d, track.CapitalizationOptions(p))
				if err != nil {
					return nil, err
				}
				return plan, nil
			}
			result, err := track.ApplyCapitalization(ctx, d, track.CapitalizationOptions(p))
			if err != nil {
				return nil, err
			}
			return result, nil
		})
	})
}

func (p capitalizationParams) operationCollectionRead() bool { return p.DryRun }
