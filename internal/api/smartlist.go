package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
)

type renameSmartlistParams struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`
}

func (s *Server) renameSmartlistOperation() operation {
	return define("smartlist.rename", true, []string{"uuid", "title"}, func(ctx context.Context, p renameSmartlistParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			if err := playlist.RenameSmartlist(ctx, d, p.UUID, p.Title); err != nil {
				return nil, err
			}
			return map[string]string{"uuid": p.UUID}, nil
		})
	})
}

type removeSmartlistParams struct {
	UUID string `json:"uuid"`
}

func (s *Server) removeSmartlistOperation() operation {
	return define("smartlist.rm", true, []string{"uuid"}, func(ctx context.Context, p removeSmartlistParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			parent, err := playlist.RemoveSmartlist(ctx, d, p.UUID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"uuid": p.UUID, "parent_id": parent}, nil
		})
	})
}
