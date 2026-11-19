package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
)

type exportPlaylistParams playlist.ExportOptions

func (p exportPlaylistParams) operationPreview() bool { return p.DryRun }

// playlist.export writes one file (identifier and path) or a folder of files
// (folder, with identifiers, tracks and name, or every playlist), optionally
// read from another library on this computer through source.
func (s *Server) exportPlaylistOperation() operation {
	return define("playlist.export", false, nil, func(ctx context.Context, p exportPlaylistParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			opts := playlist.ExportOptions(p)
			switch {
			case opts.Folder != "" && opts.DryRun:
				return optionalResult(playlist.PreviewFolderExport(ctx, d, s.config.LibraryDir, opts))
			case opts.Folder != "":
				// A failed folder export reports the files it wrote before failing.
				return optionalResult(playlist.ApplyFolderExport(ctx, d, s.config.LibraryDir, opts))
			case opts.DryRun:
				return optionalResult(playlist.PreviewExport(ctx, d, s.config.LibraryDir, opts))
			default:
				return optionalResult(playlist.ApplyExport(ctx, d, s.config.LibraryDir, opts))
			}
		})
	})
}

func (p exportPlaylistParams) operationCollectionRead() bool { return p.DryRun }

// optionalResult keeps a nil pointer from becoming a non-nil result value.
func optionalResult[T any](value *T, err error) (any, error) {
	if value == nil {
		return nil, err
	}
	return value, err
}
