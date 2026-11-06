package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
)

type importPlaylistParams playlist.ImportOptions

func (p importPlaylistParams) operationPreview() bool { return p.DryRun }

func (p importPlaylistParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

func (s *Server) importPlaylistOperation() operation {
	before := func(ctx context.Context, p importPlaylistParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return playlist.CheckImport(ctx, d, s.config.LibraryDir, playlist.ImportOptions(p))
		})
		return err
	}
	return defineChecked("playlist.import", "collection", []string{"path", "name"}, before, func(ctx context.Context, p importPlaylistParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				plan, err := playlist.PreviewImport(ctx, d, s.config.LibraryDir, playlist.ImportOptions(p))
				if err != nil {
					return nil, err
				}
				return plan, nil
			}
			result, err := playlist.ApplyImport(ctx, d, s.config.LibraryDir, playlist.ImportOptions(p))
			if err != nil {
				return nil, err
			}
			return result, nil
		})
	})
}

func (p importPlaylistParams) operationCollectionRead() bool { return p.DryRun }
