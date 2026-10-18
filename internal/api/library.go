package api

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/dj/engine-cli/internal/artwork"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/discovery"
	enginesync "github.com/dj/engine-cli/internal/sync"
	"github.com/dj/engine-cli/internal/verify"
)

func (s *Server) libraryOperations() []operation {
	return []operation{
		define("library.discover", false, nil, func(ctx context.Context, p discoverParams) (any, error) { return discovery.Discover(ctx, p.Paths) }),
		define("library.verify", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return verify.VerifyLibrary(ctx, d, s.config.LibraryDir) })
		}),
		define("library.sync", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return enginesync.SyncLibrary(ctx, d, s.config.LibraryDir) })
		}),
		define("library.backup", false, nil, func(ctx context.Context, p directoryParams) (any, error) {
			return backup.CreateBackup(ctx, s.config.LibraryDir, p.OutDir)
		}),
		define("library.backups", false, nil, func(ctx context.Context, p directoryParams) (any, error) {
			return backup.ListBackups(s.config.LibraryDir, p.OutDir)
		}),
		define("library.restore", true, []string{"path"}, func(ctx context.Context, p pathParams) (any, error) {
			return nil, backup.RestoreBackup(ctx, p.Path, s.config.LibraryDir)
		}),
		define("library.prune", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return artwork.PruneArtwork(ctx, d, filepath.Join(s.config.LibraryDir, "Artwork"))
			})
		}),
	}
}

type discoverParams struct {
	Paths []string `json:"paths"`
}
