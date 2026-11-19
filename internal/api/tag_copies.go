package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type tagCopiesParams struct {
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token"`
}

func (p tagCopiesParams) operationPreview() bool { return p.DryRun }

// tagCopiesOperation folds the "-upgraded-" copies earlier tag writes left
// behind. A dry run reviews every rename and removal as a read; an apply needs
// that review's plan_token, checked before Desktop checks, snapshots and history.
func (s *Server) tagCopiesOperation() operation {
	before := func(ctx context.Context, p tagCopiesParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return nil, track.CheckTagCopies(ctx, d, s.config.LibraryDir, p.PlanToken, s.config.ToolRunner)
		})
		return err
	}
	return defineChecked("library.tag-copies.repair", "collection", nil, before, func(ctx context.Context, p tagCopiesParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				return track.PreviewTagCopies(ctx, d, s.config.LibraryDir, s.config.ToolRunner)
			}
			return track.FoldTagCopies(ctx, d, s.config.LibraryDir, p.PlanToken, s.config.ToolRunner)
		})
	})
}
