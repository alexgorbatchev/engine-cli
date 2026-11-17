package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/performance"
)

type trackOverviewsResult struct {
	Overviews []performance.TrackOverview `json:"overviews"`
}

// trackOverviews serves the stored overview waveforms Engine DJ built for 1–500 explicit tracks in one request, in
// request order, so a track list draws Engine's own previews without one request per row.
func (s *Server) trackOverviews(w http.ResponseWriter, r *http.Request) {
	ids, ok := overviewIDs(w, r)
	if !ok {
		return
	}
	overviews, err := history.ReadSmartlistSafe(r.Context(), s.config.LibraryDir, func(ctx context.Context) ([]performance.TrackOverview, error) {
		database, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
		if err != nil {
			return nil, &collectionReadError{http.StatusServiceUnavailable, "library_unavailable", "Could not open the library. Check the configured library folder."}
		}
		defer database.Close()
		return performance.ReadOverviews(ctx, database, ids)
	})
	if err != nil {
		writeCollectionReadError(w, err)
		return
	}
	data, err := json.Marshal(trackOverviewsResult{Overviews: overviews})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_response", "response encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	serveRevalidatedContent(w, r, "overviews.json", data)
}

// overviewIDs accepts only the `ids` parameter: comma-separated positive track IDs, 1 to details.MaxItems in all.
func overviewIDs(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	ids := []int64{}
	for key, values := range r.URL.Query() {
		if key != "ids" {
			writeError(w, http.StatusBadRequest, "invalid_ids", "Use the ids query parameter.")
			return nil, false
		}
		for _, value := range values {
			for _, field := range strings.Split(value, ",") {
				id, err := strconv.ParseInt(field, 10, 64)
				if err != nil {
					writeError(w, http.StatusBadRequest, "invalid_ids", "Use positive numeric IDs.")
					return nil, false
				}
				ids = append(ids, id)
			}
		}
	}
	if err := details.Validate(ids); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_ids", err.Error())
		return nil, false
	}
	return ids, true
}
