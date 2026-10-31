package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// defineCollectionRead marks rerunnable observations of the selected library.
// Output-producing tools and operations with their own history owner stay outside
// this boundary even when their advertised effect is read-only.
func defineCollectionRead[P any](name string, required []string, run func(context.Context, P) (any, error)) operation {
	op := define(name, false, required, run)
	op.collectionRead = true
	return op
}

type collectionReadError struct {
	status        int
	code, message string
}

func (e *collectionReadError) Error() string { return e.message }

func writeCollectionReadError(w http.ResponseWriter, err error) {
	var failure *collectionReadError
	if errors.As(err, &failure) {
		writeError(w, failure.status, failure.code, failure.message)
		return
	}
	code := "library_unavailable"
	var coded *progress.Error
	if errors.As(err, &coded) {
		code = coded.Code
	}
	writeError(w, http.StatusServiceUnavailable, code, "Could not read the selected library.")
}

// readRequestTrack returns prepared data before the handler emits any headers.
// The callback must not stream, mutate, or retain database-owned resources.
func readRequestTrack[T any](s *Server, r *http.Request, read func(context.Context, *sql.DB, *db.Track) (T, error)) (T, error) {
	var zero T
	id := r.PathValue("id")
	number, err := strconv.ParseInt(id, 10, 64)
	if (err != nil || number <= 0) && !strings.HasPrefix(id, "eng:") {
		return zero, &collectionReadError{http.StatusBadRequest, "invalid_track", "Use a positive numeric track ID."}
	}
	result, err := history.ReadSmartlistSafe(r.Context(), s.config.LibraryDir, func(ctx context.Context) (T, error) {
		database, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
		if err != nil {
			return zero, &collectionReadError{http.StatusServiceUnavailable, "library_unavailable", "Could not open the library. Check the configured library folder."}
		}
		defer database.Close()
		track, err := db.FindTrack(ctx, database, id)
		if err != nil {
			return zero, &collectionReadError{http.StatusServiceUnavailable, "library_unavailable", "Could not read this track from the library."}
		}
		if track == nil {
			return zero, &collectionReadError{http.StatusNotFound, "track_not_found", "This track is no longer in the library."}
		}
		return read(ctx, database, track)
	})
	if progress.ErrorCode(err) == "smartlist_repair_pending" {
		return zero, &collectionReadError{http.StatusServiceUnavailable, "smartlist_repair_pending", "Finish the interrupted Smartlist repair before reading this library."}
	}
	return result, err
}
