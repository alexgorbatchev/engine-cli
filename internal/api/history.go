package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/duplicate"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

type undoParams struct {
	Library   string   `json:"library"`
	ID        string   `json:"id"`
	IDs       []string `json:"ids"`
	Exclude   []string `json:"exclude"`
	KeepLater bool     `json:"keep_later"`
	PlanToken string   `json:"plan_token"`
	Force     bool     `json:"force"`
	DryRun    bool     `json:"dry_run"`
}

func (p undoParams) validate() error {
	if p.ID != "" && len(p.IDs) > 0 {
		return errors.New("provide id or ids, not both")
	}
	if p.DryRun && p.PlanToken != "" {
		return errors.New("plan_token applies a reviewed undo; a dry run returns a new one")
	}
	return history.ValidateExclusions(p.Exclude)
}

func (p undoParams) request() history.UndoRequest {
	ids := p.IDs
	if len(ids) == 0 && p.ID != "" {
		ids = []string{p.ID}
	}
	return history.UndoRequest{IDs: ids, Exclude: p.Exclude, KeepLater: p.KeepLater, Force: p.Force}
}

type historyParams struct {
	Library string `json:"library"`
}

type undoStatusParams struct {
	Library   string `json:"library"`
	PlanToken string `json:"plan_token"`
}

func (s *Server) historyOperations() []operation {
	library := func(lib string) string {
		if lib == "" {
			return s.config.LibraryDir
		}
		return lib
	}
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
			return history.List(library(p.Library))
		}),
		define("library.undo.status", false, []string{"plan_token"}, func(ctx context.Context, p undoStatusParams) (any, error) {
			return history.UndoStatus(library(p.Library), p.PlanToken)
		}),
		define("library.undo", true, nil, func(ctx context.Context, p undoParams) (any, error) {
			lib := library(p.Library)
			req := p.request()
			if p.DryRun {
				return history.ReviewUndo(ctx, lib, req)
			}
			entry, err := history.Undo(ctx, lib, "", history.UndoOptions{
				IDs: req.IDs, Exclude: req.Exclude, KeepLater: req.KeepLater, PlanToken: p.PlanToken, Force: req.Force,
				Prepare: func(ctx context.Context) error {
					_, err := backup.AutoBackupIfNeeded(ctx, lib, s.config.NoBackup)
					return err
				},
			})
			if err == nil && entry != nil {
				err = s.state.RecordUndo(ctx, entry)
			}
			return entry, err
		}),
	}
}
