package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/playhistory"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type sessionParams struct {
	Session int64 `json:"session"`
}

func (p sessionParams) validate() error {
	return playhistory.ImportOptions{Session: p.Session}.Validate()
}

type importSessionParams playhistory.ImportOptions

func (p importSessionParams) validate() error { return playhistory.ImportOptions(p).Validate() }

func (p importSessionParams) operationPreview() bool { return p.DryRun }

func (p importSessionParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

// playHistory reads the plays every track record reports; nil when the library has no history.
func (s *Server) playHistory(ctx context.Context, library db.Querier) (details.History, error) {
	return playhistory.Counts(ctx, s.config.LibraryDir, library)
}

func (s *Server) sessionOperations() []operation {
	before := func(ctx context.Context, p importSessionParams, _ *boundOperation) error {
		if p.DryRun {
			return nil
		}
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			return nil, playhistory.CheckImport(ctx, s.config.LibraryDir, d, playhistory.ImportOptions(p))
		})
		return err
	}
	return []operation{
		define("session.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return operationResult(playhistory.List(ctx, s.config.LibraryDir))
		}),
		define("session.inspect", false, []string{"session"}, func(ctx context.Context, p sessionParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return operationResult(playhistory.Inspect(ctx, s.config.LibraryDir, d, p.Session))
			})
		}),
		defineChecked("session.import", "collection", []string{"session"}, before, func(ctx context.Context, p importSessionParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				if p.DryRun {
					return operationResult(playhistory.PreviewImport(ctx, s.config.LibraryDir, d, playhistory.ImportOptions(p)))
				}
				return operationResult(playhistory.ApplyImport(ctx, s.config.LibraryDir, d, playhistory.ImportOptions(p)))
			})
		}),
	}
}

// operationResult keeps a failed read from reaching callers as a typed nil pointer.
func operationResult[T any](value *T, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (s *Server) playedSessions(w http.ResponseWriter, r *http.Request) {
	result, err := playhistory.List(r.Context(), s.config.LibraryDir)
	if err != nil {
		writeHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) playedSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_session", "Use a positive numeric session ID.")
		return
	}
	library, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not open the library. Check the configured library folder.")
		return
	}
	defer library.Close()
	result, err := playhistory.Inspect(r.Context(), s.config.LibraryDir, library, id)
	if err != nil {
		writeHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeHistoryError(w http.ResponseWriter, err error) {
	var coded *progress.Error
	errors.As(err, &coded)
	switch {
	case coded != nil && coded.Code == "session_not_found":
		writeError(w, http.StatusNotFound, coded.Code, "This played session is no longer in the history.")
	case coded != nil && coded.Code == "play_history_unavailable":
		writeError(w, http.StatusNotFound, coded.Code, "This library has no play history yet.")
	default:
		writeError(w, http.StatusServiceUnavailable, "play_history_unreadable", "Could not read the library's play history.")
	}
}
