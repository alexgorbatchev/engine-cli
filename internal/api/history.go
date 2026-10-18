package api

import (
	"context"
	"database/sql"
	"github.com/dj/engine-cli/internal/duplicate"
	"github.com/dj/engine-cli/internal/history"
)

type undoParams struct {
	ID     string `json:"id"`
	Force  bool   `json:"force"`
	DryRun bool   `json:"dry_run"`
}

func (s *Server) historyOperations() []operation {
	return []operation{
		define("track.duplicate.merge", true, []string{"keep", "remove"}, func(ctx context.Context, p duplicate.MergeOptions) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return duplicate.Merge(ctx, d, p) })
		}),
		define("library.history", false, nil, func(ctx context.Context, _ emptyParams) (any, error) { return history.List(s.config.LibraryDir) }),
		define("library.undo", true, nil, func(ctx context.Context, p undoParams) (any, error) {
			return history.Undo(ctx, s.config.LibraryDir, p.ID, history.UndoOptions{Force: p.Force, DryRun: p.DryRun})
		}),
	}
}
