package api

import (
	"context"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/details"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

type detailParams struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) batchDetails(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids := []int64{}
		for key, values := range r.URL.Query() {
			if key != "ids" {
				writeError(w, http.StatusBadRequest, "invalid_ids", "Only the ids query parameter is supported.")
				return
			}
			for _, value := range values {
				for _, value := range strings.Split(value, ",") {
					id, err := strconv.ParseInt(value, 10, 64)
					if err != nil {
						writeError(w, http.StatusBadRequest, "invalid_ids", "Use positive numeric IDs.")
						return
					}
					ids = append(ids, id)
				}
			}
		}
		if err := details.Validate(ids); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_ids", err.Error())
			return
		}
		result, err := s.readDetails(r.Context(), kind, ids)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not read details from the selected library.")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (p detailParams) validate() error { return details.Validate(p.IDs) }

func (s *Server) detailOperation(kind string) operation {
	return define(kind+".inspect", false, []string{"ids"}, func(ctx context.Context, p detailParams) (any, error) {
		return s.readDetails(ctx, kind, p.IDs)
	})
}

func (s *Server) readDetails(ctx context.Context, kind string, ids []int64) (*details.Report, error) {
	d, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return details.Read(ctx, d, kind, ids)
}
