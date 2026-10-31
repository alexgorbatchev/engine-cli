package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
)

type trackColorParams struct {
	Track string `json:"track"`
}

type colorReconcileParams colors.ReconcileOptions

func (p colorReconcileParams) operationPreview() bool { return p.DryRun }

type updateTrackColorParams struct {
	Track    string `json:"track"`
	Revision string `json:"revision"`
	Color    string `json:"color" allowempty:"true"`
}

func (p updateTrackColorParams) edit() colors.Edit {
	return colors.Edit{Track: p.Track, Revision: p.Revision, Color: p.Color}
}

func (s *Server) colorOperations() []operation {
	return []operation{
		defineChecked("track.color.reconcile", "collection", []string{"revision"}, func(ctx context.Context, p colorReconcileParams, _ *boundOperation) error {
			return colors.CheckReconciliation(ctx, s.config.LibraryDir, colors.ReconcileOptions(p))
		}, func(ctx context.Context, p colorReconcileParams) (any, error) {
			result, err := colors.Reconcile(ctx, s.config.LibraryDir, colors.ReconcileOptions(p))
			if err != nil {
				return nil, err
			}
			return result, nil
		}),
		define("track.color.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				result, err := colors.ListTracks(ctx, d, s.config.LibraryDir)
				if err != nil {
					return nil, err
				}
				return result, nil
			})
		}),
		define("track.color.inspect", false, []string{"track"}, func(ctx context.Context, p trackColorParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				result, err := colors.InspectTrack(ctx, d, s.config.LibraryDir, p.Track)
				if err != nil {
					return nil, err
				}
				return result, nil
			})
		}),
		defineChecked("track.color.update", "collection", []string{"track", "revision", "color"}, func(ctx context.Context, p updateTrackColorParams, _ *boundOperation) error {
			_, err := s.database(ctx, func(d *sql.DB) (any, error) {
				return nil, colors.CheckEdit(ctx, d, s.config.LibraryDir, p.edit())
			})
			return err
		}, func(ctx context.Context, p updateTrackColorParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				result, err := colors.Assign(ctx, d, s.config.LibraryDir, p.edit())
				if err != nil {
					return nil, err
				}
				return result, nil
			})
		}),
	}
}
