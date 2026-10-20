package api

import (
	"bytes"
	"compress/zlib"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const (
	maxGridBytes    = 8 << 20
	gridHeaderBytes = 17
	gridAnchorBytes = 24
)

type beatAnchor struct {
	Sample         float64 `json:"sample"`
	Beat           int64   `json:"beat"`
	BeatsUntilNext uint32  `json:"beatsUntilNext"`
}

type beatGrid struct {
	SampleRate float64      `json:"sampleRate"`
	Anchors    []beatAnchor `json:"anchors"`
}

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
	var exists bool
	if err = database.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='PerformanceData')").Scan(&exists); err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not read track analysis.")
		return
	}
	var grid *beatGrid
	if exists {
		var blob []byte
		err = database.QueryRowContext(r.Context(), "SELECT beatData FROM PerformanceData WHERE trackId=?", track.ID).Scan(&blob)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusServiceUnavailable, "analysis_unavailable", "Could not read this track's beat grid.")
			return
		}
		if len(blob) > 0 {
			grid, err = decodeBeatGrid(blob)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "invalid_beatgrid", "The saved Engine beat grid could not be read. Reanalyze this track in Engine DJ.")
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"grid": grid})
}

// Engine's Qt framing and mixed-endian saved-grid codec are documented by
// engine-dj-app's compression.rs, beat_grid/payload.rs and beat_grid/storage.rs.
// The first vector is the fallback; the second contains the user's current grid.
func decodeBeatGrid(blob []byte) (*beatGrid, error) {
	if len(blob) < 5 {
		return nil, errors.New("missing compressed grid")
	}
	r, err := zlib.NewReader(bytes.NewReader(blob[4:]))
	if err != nil {
		return nil, fmt.Errorf("opening grid: %w", err)
	}
	defer r.Close()
	payload, err := io.ReadAll(io.LimitReader(r, maxGridBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decompressing grid: %w", err)
	}
	if len(payload) > maxGridBytes || len(payload) < gridHeaderBytes {
		return nil, errors.New("invalid grid length")
	}
	rate := math.Float64frombits(binary.BigEndian.Uint64(payload))
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return nil, errors.New("invalid sample rate")
	}
	payload = payload[gridHeaderBytes:]
	var anchors []beatAnchor
	for range 2 {
		if len(payload) < 8 {
			return nil, errors.New("missing grid vector")
		}
		count := binary.BigEndian.Uint64(payload)
		payload = payload[8:]
		if count > uint64(len(payload)/gridAnchorBytes) {
			return nil, errors.New("truncated grid vector")
		}
		anchors = make([]beatAnchor, 0, count)
		for range count {
			anchor := beatAnchor{
				Sample:         math.Float64frombits(binary.LittleEndian.Uint64(payload)),
				Beat:           int64(binary.LittleEndian.Uint64(payload[8:])),
				BeatsUntilNext: binary.LittleEndian.Uint32(payload[16:]),
			}
			if math.IsNaN(anchor.Sample) || math.IsInf(anchor.Sample, 0) || anchor.Beat >= 1<<53 || anchor.Beat <= -(1<<53) {
				return nil, errors.New("invalid beat anchor")
			}
			anchors = append(anchors, anchor)
			payload = payload[gridAnchorBytes:]
		}
	}
	if len(anchors) < 2 {
		return nil, nil
	}
	for i := 0; i+1 < len(anchors); i++ {
		if anchors[i].Sample >= anchors[i+1].Sample || anchors[i].BeatsUntilNext == 0 {
			return nil, errors.New("invalid grid segment")
		}
	}
	return &beatGrid{SampleRate: rate, Anchors: anchors}, nil
}
