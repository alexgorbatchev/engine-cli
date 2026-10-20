package api

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/migrate"
)

func (s *Server) migrationOperations() []operation {
	// Mutates describes source-library mutations, as with library.backup.
	return []operation{define("migrate.export", false, nil, func(ctx context.Context, p migrate.Options) (any, error) {
		if p.Source == "" {
			p.Source = s.config.LibraryDir
		}
		return migrate.Export(ctx, p)
	})}
}
