package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
)

type exportPlaylistParams playlist.ExportOptions

func (p exportPlaylistParams) operationPreview() bool { return p.DryRun }

func (s *Server) exportPlaylistOperation() operation {
	return define("playlist.export", false, []string{"identifier", "path"}, func(ctx context.Context, p exportPlaylistParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				plan, err := playlist.PreviewExport(ctx, d, s.config.LibraryDir, playlist.ExportOptions(p))
				if err != nil {
					return nil, err
				}
				return plan, nil
			}
			result, err := playlist.ApplyExport(ctx, d, s.config.LibraryDir, playlist.ExportOptions(p))
			if err != nil {
				return nil, err
			}
			return result, nil
		})
	})
}
