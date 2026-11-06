package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alexgorbatchev/engine-cli/internal/beatgrid"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/nateranda/djtools/engine"
)

type analyzeParams track.AnalysisRequest

func (p analyzeParams) validate() error { return track.AnalysisRequest(p).Validate() }

type gridUpdateParams struct {
	Identifier string              `json:"identifier"`
	Anchors    []engine.BeatAnchor `json:"anchors"`
}

func (p gridUpdateParams) validate() error {
	if len(p.Anchors) == 0 {
		return errors.New("anchors are required")
	}
	return nil
}

func (s *Server) analysisOperations() []operation {
	return []operation{
		define("track.analyze", true, []string{"identifiers"}, func(ctx context.Context, p analyzeParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return track.Analyze(ctx, d, s.config.LibraryDir, track.AnalysisRequest(p), s.writeGuard)
			})
		}),
		define("track.grid.update", true, []string{"identifier", "anchors"}, func(ctx context.Context, p gridUpdateParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				t, err := db.FindTrack(ctx, d, p.Identifier)
				if err != nil {
					return nil, err
				}
				if t == nil {
					return nil, &progress.Error{Code: "not_found", Cause: sql.ErrNoRows}
				}
				tx, err := d.BeginTx(ctx, nil)
				if err != nil {
					return nil, err
				}
				defer tx.Rollback()
				if err := beatgrid.Update(ctx, tx, t.ID, p.Anchors); err != nil {
					return nil, err
				}
				if err := tx.Commit(); err != nil {
					return nil, err
				}
				return changedTrack{TrackID: t.ID}, nil
			})
		}),
	}
}

// writeGuard rechecks Engine DJ Desktop before each write of a long collection job.
func (s *Server) writeGuard(ctx context.Context) error {
	r, err := s.checkSystem(ctx, nil)
	if err != nil {
		return err
	}
	return systemcheck.AllowMutation(r)
}

// analyzeImports analyzes tracks an import just added, in the second half of the job's progress. Analysis failures
// are reported per track and never undo the import.
func (s *Server) analyzeImports(ctx context.Context, d *sql.DB, ids []int64, bpmRange string) (map[int64]track.AnalyzedTrack, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	report, err := track.AnalyzeImported(progress.Range(ctx, 50, 100), d, s.config.LibraryDir, ids, bpmRange, s.writeGuard)
	if report == nil {
		return nil, err
	}
	// Tracks analyzed before a failure keep their outcomes alongside the error.
	byID := make(map[int64]track.AnalyzedTrack, len(report.Tracks))
	for _, t := range report.Tracks {
		byID[t.TrackID] = t
	}
	return byID, err
}

type gridEditRequest struct {
	Anchors []engine.BeatAnchor `json:"anchors"`
	beatgrid.Edit
}

// trackGridEdit computes a grid edit for review without saving it. The draft defaults to the saved grid; resets restore
// the saved default grid.
func (s *Server) trackGridEdit(w http.ResponseWriter, r *http.Request) {
	var request gridEditRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "beatgrid_edit_invalid", "Provide a grid edit as a JSON object.")
		return
	}
	grid, err := readRequestTrack(s, r, func(ctx context.Context, database *sql.DB, t *db.Track) (*beatgrid.Grid, error) {
		data, err := beatgrid.Load(ctx, database, t.ID)
		if err != nil {
			return nil, err
		}
		if data == nil {
			return nil, &collectionReadError{http.StatusConflict, "beatgrid_missing", "Analyze this track's beat grid before editing it."}
		}
		if locked, err := beatgrid.Locked(ctx, database, t.ID); err != nil {
			return nil, err
		} else if locked {
			return nil, beatgrid.ErrLocked()
		}
		grid, err := beatgrid.Apply(beatgrid.SourceOf(*data), request.Anchors, request.Edit)
		if err != nil {
			return nil, err
		}
		return &grid, nil
	})
	if err != nil {
		var readErr *collectionReadError
		if errors.As(err, &readErr) {
			writeCollectionReadError(w, err)
		} else {
			writeGridError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grid": grid})
}

func writeGridError(w http.ResponseWriter, err error) {
	var coded *progress.Error
	if !errors.As(err, &coded) {
		writeError(w, http.StatusServiceUnavailable, "analysis_unavailable", "Could not read this track's beat grid.")
		return
	}
	status := http.StatusUnprocessableEntity
	switch coded.Code {
	case "beatgrid_edit_invalid":
		status = http.StatusBadRequest
	case "beatgrid_locked":
		status = http.StatusConflict
	}
	writeError(w, status, coded.Code, progress.PublicError(coded.Cause))
}
