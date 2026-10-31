package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/rekordbox"
)

type importRekordboxParams rekordbox.Options

func (p importRekordboxParams) operationPreview() bool { return p.DryRun }

func (p importRekordboxParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

func (s *Server) importRekordboxOperation() operation {
	before := func(ctx context.Context, p importRekordboxParams, bound *boundOperation) error {
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			prepared, err := rekordbox.Prepare(ctx, d, s.config.LibraryDir, rekordbox.Options(p))
			if err != nil {
				return nil, err
			}
			bound.run = func(ctx context.Context) (any, error) {
				if p.DryRun {
					review, err := prepared.Review()
					if err != nil {
						return nil, err
					}
					return &review, nil
				}
				return s.database(ctx, func(d *sql.DB) (any, error) {
					result, err := prepared.Apply(ctx, d)
					if err != nil {
						return nil, err
					}
					return &result, nil
				})
			}
			return nil, nil
		})
		return err
	}
	return defineChecked("migrate.import", "collection", []string{"path", "name"}, before, func(context.Context, importRekordboxParams) (any, error) {
		return nil, errors.New("Rekordbox import was not prepared")
	})
}
