package api

import (
	"context"
	"github.com/dj/engine-cli/internal/systemcheck"
	"net/http"
	"strings"
)

var querySystem = systemcheck.Run

func (s *Server) checkSystem(ctx context.Context, checks []string) (*systemcheck.Report, error) {
	if s.config.CheckSystem != nil {
		return s.config.CheckSystem(ctx, checks)
	}
	return querySystem(ctx, checks)
}

type checkParams struct {
	Checks []string `json:"checks"`
}

func (p checkParams) validate() error { return systemcheck.Validate(p.Checks) }

func (s *Server) systemOperations() []operation {
	return []operation{define("system.check", false, nil, func(ctx context.Context, p checkParams) (any, error) { return s.checkSystem(ctx, p.Checks) })}
}

func (s *Server) systemCheck(w http.ResponseWriter, r *http.Request) {
	checks := []string{}
	for key, values := range r.URL.Query() {
		if key != "checks" {
			writeError(w, http.StatusBadRequest, "invalid_checks", "Only the checks query parameter is supported.")
			return
		}
		for _, v := range values {
			checks = append(checks, strings.Split(v, ",")...)
		}
	}
	if err := systemcheck.Validate(checks); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_checks", err.Error())
		return
	}
	result, err := s.checkSystem(r.Context(), checks)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "check_unavailable", "Could not complete the system checks. Retry the request.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
