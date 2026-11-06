package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type findReplaceParams track.FindReplaceOptions

func (p findReplaceParams) validate() error        { return track.FindReplaceOptions(p).Validate() }
func (p findReplaceParams) operationPreview() bool { return p.DryRun }
func (p findReplaceParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

func (s *Server) findReplaceOperation() operation {
	before := func(ctx context.Context, p findReplaceParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) { return track.CheckFindReplace(ctx, d, track.FindReplaceOptions(p)) })
		return err
	}
	return defineChecked("track.batch.find-replace", "collection", []string{"identifiers", "fields", "find"}, before, func(ctx context.Context, p findReplaceParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				plan, err := track.PreviewFindReplace(ctx, d, track.FindReplaceOptions(p))
				if err != nil {
					return nil, err
				}
				return plan, nil
			}
			result, err := track.ApplyFindReplace(ctx, d, track.FindReplaceOptions(p))
			if err != nil {
				return nil, err
			}
			return result, nil
		})
	})
}

func (p findReplaceParams) operationCollectionRead() bool { return p.DryRun }
