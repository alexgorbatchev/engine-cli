package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type artworkImportParams struct {
	Identifiers []string `json:"identifiers"`
	DryRun      bool     `json:"dry_run"`
	PlanToken   string   `json:"plan_token"`
}

func (p artworkImportParams) validate() error        { return track.ValidateArtworkImport(p.Identifiers) }
func (p artworkImportParams) operationPreview() bool { return p.DryRun }
func (artworkImportParams) operationLimits() map[string]int {
	return itemLimits(track.MaxArtworkImports, "identifiers")
}

func (s *Server) artworkImportOptions() track.ArtworkImportOptions {
	return track.ArtworkImportOptions{Library: s.config.LibraryDir, Runner: s.config.ToolRunner}
}

// artworkImportOperation sets library covers from the art embedded in each
// track's own audio file, without rewriting the audio. dry_run reviews each
// track's file cover and whether it replaces the library's; the import applies
// exactly that review, bound to its plan_token, as one history entry. Review
// checks run before Desktop checks, snapshots and history, so a refused
// import leaves none.
func (s *Server) artworkImportOperation() operation {
	before := func(ctx context.Context, p artworkImportParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return nil, track.CheckArtworkImport(ctx, d, p.Identifiers, p.PlanToken, s.artworkImportOptions())
		})
		return err
	}
	return defineChecked("track.artwork.import", "collection", []string{"identifiers"}, before, func(ctx context.Context, p artworkImportParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if p.DryRun {
				return track.PreviewArtworkImport(ctx, d, p.Identifiers, s.artworkImportOptions())
			}
			return track.ApplyArtworkImport(ctx, d, p.Identifiers, p.PlanToken, s.artworkImportOptions())
		})
	})
}
