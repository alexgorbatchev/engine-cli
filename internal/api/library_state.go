package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/state"
)

type libraryPreferenceParams struct {
	Path   string  `json:"path"`
	Name   *string `json:"name"`
	Hidden *bool   `json:"hidden"`
}

func (p libraryPreferenceParams) validate() error {
	if !filepath.IsAbs(p.Path) || (p.Name == nil && p.Hidden == nil) {
		return fmt.Errorf("provide an absolute path and a name or hidden preference")
	}
	return nil
}

func (s *Server) libraryStateOperations() []operation {
	return []operation{
		define("library.preferences.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) { return s.state.List(ctx) }),
		defineEffect("library.preferences.update", "state", []string{"path"}, func(ctx context.Context, p libraryPreferenceParams) (any, error) {
			if err := s.state.Update(ctx, p.Path, p.Name, p.Hidden); err != nil {
				return nil, err
			}
			return s.state.List(ctx)
		}),
	}
}

func (s *Server) libraryPreferences(w http.ResponseWriter, r *http.Request) {
	entries, err := s.state.List(r.Context())
	if err != nil {
		writeError(w, 500, "state_unavailable", "Library preferences could not be read.")
		return
	}
	writeJSON(w, 200, entries)
}

func (s *Server) updateLibraryPreferences(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return
	}
	raw, err := decodeRequest(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err == nil {
		op := s.operations["library.preferences.update"]
		var bound *boundOperation
		bound, err = op.bind(json.RawMessage(raw))
		if err == nil {
			var result any
			result, err = bound.run(r.Context())
			if err == nil {
				writeJSON(w, 200, result)
				return
			}
			if !errors.Is(err, state.ErrInvalidPreference) {
				writeError(w, http.StatusInternalServerError, "state_unavailable", "Library preferences could not be saved.")
				return
			}
		}
	}
	writeError(w, 400, "invalid_parameters", "The library preference could not be saved.")
}
