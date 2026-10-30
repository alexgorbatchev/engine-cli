// Package beatgrid converts analysis results into Engine beat grids, computes reviewed grid edits, and stores grids
// in PerformanceData with the track's matching BPM.
package beatgrid

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/alexgorbatchev/engine-cli/internal/analysis"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

// Engine's grid editor clamps tempo edits to this range.
const (
	MinBPM = 30
	MaxBPM = 250
)

// Grid is an editable beat grid in its saved sample rate.
type Grid struct {
	SampleRate float64             `json:"sampleRate"`
	BPM        float64             `json:"bpm"`
	Anchors    []engine.BeatAnchor `json:"anchors"`
}

func newGrid(sampleRate float64, anchors []engine.BeatAnchor) Grid {
	return Grid{SampleRate: sampleRate, BPM: engine.BeatGridBPM(sampleRate, anchors), Anchors: anchors}
}

func gridError(code, message string) error {
	return &progress.Error{Code: code, Cause: errors.New(message)}
}

// FromAnalysis converts fitted beats in seconds into anchors at sampleRate.
func FromAnalysis(tempo analysis.Tempo, sampleRate float64) []engine.BeatAnchor {
	anchors := make([]engine.BeatAnchor, len(tempo.Anchors))
	for i, beat := range tempo.Anchors {
		anchors[i] = engine.BeatAnchor{Sample: beat.Time * sampleRate, Beat: beat.Index}
		if i > 0 {
			anchors[i-1].BeatsUntilNext = uint32(beat.Index - tempo.Anchors[i-1].Index)
		}
	}
	return anchors
}

// Validate checks anchor consistency and Engine's editable tempo range on every segment.
func Validate(sampleRate float64, anchors []engine.BeatAnchor) error {
	if err := engine.ValidateBeatAnchors(anchors); err != nil {
		return gridError("beatgrid_invalid", err.Error())
	}
	for i := 0; i+1 < len(anchors); i++ {
		bpm := segmentBPM(sampleRate, anchors, i)
		if bpm < MinBPM || bpm > MaxBPM {
			return gridError("beatgrid_tempo_range", fmt.Sprintf("segment %d would be %.2f BPM; keep tempos between %d and %d BPM", i+1, bpm, MinBPM, MaxBPM))
		}
	}
	return nil
}

func segmentBPM(sampleRate float64, anchors []engine.BeatAnchor, i int) float64 {
	return float64(anchors[i].BeatsUntilNext) / (anchors[i+1].Sample - anchors[i].Sample) * sampleRate * 60
}

// Load reads a track's saved beat data. A missing table, row, or blob returns nil.
func Load(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, trackID int64) (*engine.BeatData, error) {
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='PerformanceData')").Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	var blob []byte
	err := q.QueryRowContext(ctx, "SELECT beatData FROM PerformanceData WHERE trackId=?", trackID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) || err == nil && len(blob) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := engine.DecodeBeatData(blob)
	if err != nil {
		return nil, &progress.Error{Code: "invalid_beatgrid", Cause: err}
	}
	// The final anchor ends the grid; Engine ignores any count stored on it.
	for _, anchors := range [][]engine.BeatAnchor{data.Default, data.Adjusted} {
		if len(anchors) > 0 {
			anchors[len(anchors)-1].BeatsUntilNext = 0
		}
	}
	return &data, nil
}

// Save writes beat data. Engine creates PerformanceData rows with tracks; a missing row is inserted for libraries
// created without them.
func Save(ctx context.Context, tx *sql.Tx, trackID int64, data engine.BeatData) error {
	blob, err := engine.EncodeBeatData(data)
	if err != nil {
		return gridError("beatgrid_invalid", err.Error())
	}
	result, err := tx.ExecContext(ctx, "UPDATE PerformanceData SET beatData=? WHERE trackId=?", blob, trackID)
	if err != nil {
		return fmt.Errorf("saving beat grid: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		if _, err := tx.ExecContext(ctx, "INSERT INTO PerformanceData(trackId, beatData) VALUES(?, ?)", trackID, blob); err != nil {
			return fmt.Errorf("saving beat grid: %w", err)
		}
	}
	return nil
}

// SaveBPM stores the precise analyzed tempo and Engine's rounded integer tempo.
func SaveBPM(ctx context.Context, tx *sql.Tx, trackID int64, bpm float64) error {
	if _, err := tx.ExecContext(ctx, "UPDATE Track SET bpm=?, bpmAnalyzed=? WHERE id=?", int64(math.Round(bpm)), bpm, trackID); err != nil {
		return fmt.Errorf("saving track BPM: %w", err)
	}
	return nil
}

// Locked reports whether Engine DJ has locked a track's beat grid against changes.
func Locked(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, trackID int64) (bool, error) {
	var locked sql.NullBool
	if err := q.QueryRowContext(ctx, "SELECT isBeatGridLocked FROM Track WHERE id=?", trackID).Scan(&locked); err != nil {
		return false, fmt.Errorf("reading the beat-grid lock: %w", err)
	}
	return locked.Bool, nil
}

// ErrLocked refuses edits to a beat grid locked in Engine DJ.
func ErrLocked() error {
	return gridError("beatgrid_locked", "unlock the beat grid in Engine DJ before editing it")
}

// Update replaces the adjusted grid of a track's saved beat data, keeping the default grid Engine resets to and the
// saved header and metadata, and sets the track's BPM to the edited grid's whole-grid tempo, as Engine reports it.
func Update(ctx context.Context, tx *sql.Tx, trackID int64, anchors []engine.BeatAnchor) error {
	data, err := Load(ctx, tx, trackID)
	if err != nil {
		return err
	}
	if data == nil {
		return gridError("beatgrid_missing", "analyze the track's beat grid before editing it")
	}
	if locked, err := Locked(ctx, tx, trackID); err != nil {
		return err
	} else if locked {
		return ErrLocked()
	}
	if err := Validate(data.SampleRate, anchors); err != nil {
		return err
	}
	// Saved grids cover the whole track, as every computed edit does.
	if end := anchors[len(anchors)-1].Sample; end < data.SampleLimit {
		return gridError("beatgrid_invalid", "the grid must extend to the end of the track")
	}
	data.Adjusted = anchors
	if engine.ValidateBeatAnchors(data.Default) != nil {
		// Engine restores the default grid on reset; an unusable one would reset to its built-in fallback.
		data.Default = append([]engine.BeatAnchor(nil), anchors...)
	}
	data.IsSet = true
	if err := Save(ctx, tx, trackID, *data); err != nil {
		return err
	}
	return SaveBPM(ctx, tx, trackID, engine.BeatGridBPM(data.SampleRate, anchors))
}

// Current returns the adjusted grid Engine plays and edits.
func Current(data engine.BeatData) Grid {
	return newGrid(data.SampleRate, data.Adjusted)
}
