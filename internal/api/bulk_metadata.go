package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

// batchMetadataOperation reviews a batch edit with dry_run, and applies it either bound to a reviewed plan_token or
// at once for an in-place edit. Selection and review checks run before Desktop checks, snapshots and history.
func (s *Server) batchMetadataOperation() operation {
	before := func(ctx context.Context, p batchMetadataParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) { return nil, track.CheckMetadataBatch(ctx, d, track.BatchMetadataEdit(p)) })
		return err
	}
	return defineChecked("track.batch.update", "collection", []string{"identifiers"}, before, func(ctx context.Context, p batchMetadataParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				return track.PreviewMetadataBatch(ctx, d, track.BatchMetadataEdit(p))
			}
			return track.EditMetadataBatch(ctx, d, track.BatchMetadataEdit(p))
		})
	})
}
