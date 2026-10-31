package api

import (
	"context"
	"database/sql"
	"github.com/alexgorbatchev/engine-cli/internal/duplicate"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

type undoParams struct {
	Library string `json:"library"`
	ID      string `json:"id"`
	Force   bool   `json:"force"`
	DryRun  bool   `json:"dry_run"`
}
type historyParams struct {
	Library string `json:"library"`
}

func (s *Server) historyOperations() []operation {
	return []operation{
		defineChecked("track.duplicate.merge", "collection", nil, func(ctx context.Context, p duplicate.MergeOptions, _ *boundOperation) error {
			_, err := s.database(ctx, func(d *sql.DB) (any, error) { return nil, duplicate.CheckMerge(ctx, d, p) })
			return err
		}, func(ctx context.Context, p duplicate.MergeOptions) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				result, err := duplicate.Merge(ctx, d, p)
				if err != nil {
					return nil, err
				}
				return result, nil
			})
		}),
		define("library.history", false, nil, func(ctx context.Context, p historyParams) (any, error) {
			lib := p.Library
			if lib == "" {
				lib = s.config.LibraryDir
			}
			return history.List(lib)
		}),
		define("library.undo", true, nil, func(ctx context.Context, p undoParams) (any, error) {
			lib := p.Library
			if lib == "" {
				lib = s.config.LibraryDir
			}
			entry, err := history.Undo(ctx, lib, p.ID, history.UndoOptions{Force: p.Force, DryRun: p.DryRun})
			if err == nil && !p.DryRun && entry != nil {
				err = s.state.RecordUndo(ctx, entry)
			}
			return entry, err
		}),
	}
}
