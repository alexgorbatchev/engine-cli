package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/smartlist"
)

type getSmartlistParams struct {
	UUID     string `json:"uuid"`
	TimeZone string `json:"time_zone"`
}

func (s *Server) smartlistRuleOperations() []operation {
	return []operation{
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
