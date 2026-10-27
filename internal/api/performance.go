package api

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/engine"
)

type savedCue struct {
	Slot     int     `json:"slot"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

type savedLoop struct {
	Slot  int     `json:"slot"`
	Name  string  `json:"name"`
	Color string  `json:"color"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type savedPerformance struct {
	Cues  []savedCue  `json:"cues"`
	Loops []savedLoop `json:"loops"`
}

func (s *Server) trackSavedPerformance(w http.ResponseWriter, r *http.Request) {
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
	var exists bool
	if err := database.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='PerformanceData')").Scan(&exists); err != nil {
		writeError(w, http.StatusServiceUnavailable, "saved_performance_unavailable", "Could not read saved cues and loops.")
		return
	}
	result := savedPerformance{Cues: []savedCue{}, Loops: []savedLoop{}}
	if exists {
		var beat, cues, loops []byte
		err := database.QueryRowContext(r.Context(), "SELECT beatData, quickCues, loops FROM PerformanceData WHERE trackId=?", track.ID).Scan(&beat, &cues, &loops)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusServiceUnavailable, "saved_performance_unavailable", "Could not read saved cues and loops.")
			return
		}
		decoded, err := engine.DecodeSavedPerformance(beat, cues, loops)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_saved_performance", "Could not decode saved cues and loops.")
			return
		}
		for _, cue := range decoded.HotCues {
			result.Cues = append(result.Cues, savedCue{Slot: cue.Position, Name: cue.Name, Color: cue.Color, Position: cue.Offset})
		}
		for _, loop := range decoded.Loops {
			result.Loops = append(result.Loops, savedLoop{Slot: loop.Position, Name: loop.Name, Color: loop.Color, Start: loop.Start, End: loop.End})
		}
	}
	writeJSON(w, http.StatusOK, result)
}
