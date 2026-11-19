package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type tagEditParams struct {
	Edits       []track.TagEdit `json:"edits"`
	DryRun      bool            `json:"dry_run"`
	PlanToken   string          `json:"plan_token"`
	AutoInstall bool            `json:"auto_install"`
}

func (p tagEditParams) validate() error        { return track.ValidateTagEdits(p.Edits) }
func (p tagEditParams) operationPreview() bool { return p.DryRun }
func (tagEditParams) operationLimits() map[string]int {
	return itemLimits(track.MaxTagEdits, "edits")
}

func (s *Server) tagEditOptions(p tagEditParams) track.TagEditOptions {
	return track.TagEditOptions{Library: s.config.LibraryDir, AutoInstall: p.AutoInstall, Runner: s.config.ToolRunner}
}

// tagEditOperation reviews per-file embedded tag values from the files
// themselves with dry_run, and writes exactly that review, bound to its
// plan_token, in place and without online lookup. Review checks run before
// Desktop checks, snapshots and history, so a refused write leaves none.
func (s *Server) tagEditOperation() operation {
	before := func(ctx context.Context, p tagEditParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return nil, track.CheckTagEdits(ctx, d, p.Edits, p.PlanToken, s.tagEditOptions(p))
		})
		return err
	}
	return defineChecked("track.tags.edit", "collection", []string{"edits"}, before, func(ctx context.Context, p tagEditParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				return track.PreviewTagEdits(ctx, d, p.Edits, s.tagEditOptions(p))
			}
			return track.ApplyTagEdits(ctx, d, p.Edits, p.PlanToken, s.tagEditOptions(p))
		})
	})
}
