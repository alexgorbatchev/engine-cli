package api

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/migrate"
)

type exportRekordboxParams migrate.Options

// A dry run reads the source and the destination's free space only, so live
// reviews run as reads and never queue behind writes.
func (p exportRekordboxParams) operationPreview() bool { return p.DryRun }

func (s *Server) migrationOperations() []operation {
	// Mutates describes source-library mutations, as with library.backup.
	return []operation{s.importRekordboxOperation(), define("migrate.export", false, nil, func(ctx context.Context, p exportRekordboxParams) (any, error) {
		if p.Source == "" {
			p.Source = s.config.LibraryDir
		}
		if p.DryRun {
			return migrate.Review(ctx, migrate.Options(p))
		}
		return migrate.Export(ctx, migrate.Options(p))
	})}
}
