package api

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/beatgrid"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

// trackBeatGrid reads the adjusted grid Engine plays and edits. Grids with fewer than two anchors are reported as
// missing, as Engine replaces them with its fallback.
func (s *Server) trackBeatGrid(w http.ResponseWriter, r *http.Request) {
	track := s.requestTrack(w, r)
	if track == nil {
		return
	}
	database, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not open the library.")
		return
	}
	defer database.Close()
	data, err := beatgrid.Load(r.Context(), database, track.ID)
	var coded *progress.Error
	if errors.As(err, &coded) && coded.Code == "invalid_beatgrid" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_beatgrid", "The saved Engine beat grid could not be read. Reanalyze this track.")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "analysis_unavailable", "Could not read this track's beat grid.")
		return
	}
	if data == nil || len(data.Adjusted) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"grid": nil})
		return
	}
	if err := engine.ValidateBeatAnchors(data.Adjusted); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_beatgrid", "The saved Engine beat grid could not be read. Reanalyze this track.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grid": beatgrid.Current(*data)})
}
