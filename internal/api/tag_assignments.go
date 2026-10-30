package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func (s *Server) tagAssignmentsOperation() operation {
	return define("track.custom-tags.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		result, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return tags.ListTracks(ctx, d, s.config.LibraryDir)
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}
