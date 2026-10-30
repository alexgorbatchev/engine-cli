package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/performance"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

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
	result, err := performance.Inspect(r.Context(), database, s.config.LibraryDir, strconv.FormatInt(track.ID, 10))
	if err != nil {
		status, code := http.StatusServiceUnavailable, "saved_performance_unavailable"
		var coded *progress.Error
		if errors.As(err, &coded) && coded.Code == "invalid_saved_performance" {
			status, code = http.StatusUnprocessableEntity, coded.Code
		}
		writeError(w, status, code, "Could not read saved cues and loops.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type cueInspectParams struct {
	Identifier string `json:"identifier"`
}
type cueClearParams struct {
	Identifier string `json:"identifier"`
	Slot       int    `json:"slot"`
	Revision   string `json:"revision"`
}
type cueSetParams struct {
	cueClearParams
	Position float64 `json:"position"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
}

func (p cueClearParams) edit() performance.Edit {
	return performance.Edit{Identifier: p.Identifier, Slot: p.Slot, Revision: p.Revision, Clear: true}
}
func (p cueSetParams) edit() performance.Edit {
	return performance.Edit{Identifier: p.Identifier, Slot: p.Slot, Revision: p.Revision, Position: p.Position, Name: p.Name, Color: p.Color}
}

func cueWriteOperation[P interface{ edit() performance.Edit }](s *Server, name string, required []string) operation {
	return defineChecked(name, "collection", required, func(ctx context.Context, p P, bound *boundOperation) error {
		_, err := s.database(ctx, func(d *sql.DB) (any, error) {
			prepared, err := performance.Prepare(ctx, d, s.config.LibraryDir, p.edit())
			if err != nil {
				return nil, err
			}
			bound.run = func(ctx context.Context) (any, error) {
				return s.database(ctx, func(d *sql.DB) (any, error) { return prepared.Apply(ctx, d) })
			}
			return nil, nil
		})
		return err
	}, func(context.Context, P) (any, error) { return nil, errors.New("cue write was not prepared") })
}

func (s *Server) cueOperations() []operation {
	return []operation{
		define("track.cue.inspect", false, []string{"identifier"}, func(ctx context.Context, p cueInspectParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return performance.Inspect(ctx, d, s.config.LibraryDir, p.Identifier) })
		}),
		cueWriteOperation[cueSetParams](s, "track.cue.set", []string{"identifier", "slot", "position", "color", "revision"}),
		cueWriteOperation[cueClearParams](s, "track.cue.clear", []string{"identifier", "slot", "revision"}),
	}
}
