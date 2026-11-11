package api

import (
	"context"
	"database/sql"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"

	"github.com/alexgorbatchev/engine-cli/internal/smartlist"
)

type getSmartlistParams struct {
	UUID     string `json:"uuid"`
	TimeZone string `json:"time_zone"`
}

func (s *Server) smartlistRuleOperations() []operation {
	return []operation{
		defineCollectionRead("smartlist.convert.preview", []string{"uuid"}, func(ctx context.Context, p getSmartlistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return smartlist.PreviewConversion(ctx, d, p.UUID, s.config.LibraryDir)
			})
		}),
		define("smartlist.convert", true, []string{"uuid", "plan_token"}, func(ctx context.Context, p smartlist.ConversionOptions) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return smartlist.Convert(ctx, d, s.config.LibraryDir, p)
			})
		}),
		defineCollectionRead("smartlist.tracks", []string{"uuid"}, func(ctx context.Context, p getSmartlistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				ids, err := smartlist.MatchingIDs(ctx, d, p.UUID, s.config.LibraryDir)
				if err != nil {
					return nil, err
				}
				tracks, err := db.GetTracksByIDs(ctx, d, ids)
				if err != nil {
					return nil, err
				}
				all := make([]*db.Track, len(tracks))
				for i := range tracks {
					all[i] = &tracks[i]
				}
				history, err := s.playHistory(ctx, d)
				if err != nil {
					return nil, err
				}
				return details.Tracks(ctx, d, history, all...)
			})
		}),
		define("smartlist.catalog", false, nil, func(context.Context, emptyParams) (any, error) { return smartlist.RuleCatalog(), nil }),
		defineCollectionRead("smartlist.get", []string{"uuid"}, func(ctx context.Context, p getSmartlistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return smartlist.Get(ctx, d, p.UUID, p.TimeZone) })
		}),
		define("smartlist.create", true, []string{"title", "match", "rules"}, func(ctx context.Context, p smartlist.CreateOptions) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				id, err := smartlist.Create(ctx, d, p)
				if err != nil {
					return nil, err
				}
				return map[string]string{"uuid": id}, nil
			})
		}),
		define("smartlist.update", true, []string{"uuid", "title", "revision", "match", "rules"}, func(ctx context.Context, p smartlist.UpdateOptions) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				if err := smartlist.Update(ctx, d, p); err != nil {
					return nil, err
				}
				return map[string]string{"uuid": p.UUID}, nil
			})
		}),
	}
}
