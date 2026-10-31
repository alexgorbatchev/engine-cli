package api

import (
	"context"
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

type detailParams struct {
	IDs  []int64  `json:"ids"`
	Refs []string `json:"refs"`
}

func (s *Server) batchDetails(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids := []int64{}
		refs := []string{}
		for key, values := range r.URL.Query() {
			if key == "refs" {
				for _, value := range values {
					refs = append(refs, strings.Split(value, ",")...)
				}
				continue
			}
			if key != "ids" {
				writeError(w, http.StatusBadRequest, "invalid_ids", "Use either ids or refs query parameters.")
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
		p := detailParams{IDs: ids, Refs: refs}
		if err := p.validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_ids", err.Error())
			return
		}
		result, err := s.readSelectedDetails(r.Context(), kind, p)
		if err != nil {
			writeCollectionReadError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (p detailParams) validate() error {
	if len(p.Refs) > 0 {
		if len(p.IDs) > 0 {
			return fmt.Errorf("choose refs or ids")
		}
		return details.ValidateRefs(p.Refs)
	}
	return details.Validate(p.IDs)
}

func (s *Server) detailOperation(kind string) operation {
	return define(kind+".inspect", false, nil, func(ctx context.Context, p detailParams) (any, error) {
		return s.readSelectedDetails(ctx, kind, p)
	})
}

func (s *Server) readSelectedDetails(ctx context.Context, kind string, p detailParams) (*details.Report, error) {
	return history.ReadSmartlistSafe(ctx, s.config.LibraryDir, func(ctx context.Context) (*details.Report, error) {
		return s.readSelectedDetailsLocked(ctx, kind, p)
	})
}

func (s *Server) readSelectedDetailsLocked(ctx context.Context, kind string, p detailParams) (*details.Report, error) {
	if len(p.Refs) == 0 {
		return s.readDetails(ctx, kind, p.IDs)
	}
	d, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	history, err := s.playHistory(ctx, d)
	if err != nil {
		return nil, err
	}
	return details.ReadRefs(ctx, d, history, kind, p.Refs)
}

func (s *Server) readDetails(ctx context.Context, kind string, ids []int64) (*details.Report, error) {
	d, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	history, err := s.playHistory(ctx, d)
	if err != nil {
		return nil, err
	}
	return details.Read(ctx, d, history, kind, ids)
}
