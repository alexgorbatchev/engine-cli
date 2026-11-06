package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
)

type reorderPlaylistParams playlist.ReorderOptions

func (p reorderPlaylistParams) operationPreview() bool { return p.DryRun }

func (p reorderPlaylistParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

func (s *Server) reorderPlaylistOperation() operation {
	before := func(ctx context.Context, p reorderPlaylistParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return operationResult(playlist.CheckReorder(ctx, d, s.config.LibraryDir, playlist.ReorderOptions(p)))
		})
		return err
	}
	return defineChecked("playlist.reorder", "collection", []string{"playlist", "rows", "drop"}, before, func(ctx context.Context, p reorderPlaylistParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				return operationResult(playlist.PreviewReorder(ctx, d, s.config.LibraryDir, playlist.ReorderOptions(p)))
			}
			return operationResult(playlist.ApplyReorder(ctx, d, s.config.LibraryDir, playlist.ReorderOptions(p)))
		})
	})
}

func (p reorderPlaylistParams) operationCollectionRead() bool { return p.DryRun }
