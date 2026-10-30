package performance

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
)

type Edit struct {
	Identifier string  `json:"identifier"`
	Slot       int     `json:"slot"`
	Position   float64 `json:"position"`
	Name       string  `json:"name"`
	Color      string  `json:"color"`
	Revision   string  `json:"revision"`
	Clear      bool    `json:"-"`
}

type Result struct {
	TrackID  int64  `json:"track_id"`
	Slot     int    `json:"slot"`
	Revision string `json:"revision"`
	Changed  bool   `json:"changed"`
}

// Prepared retains the exact reviewed state across the safety snapshot and transaction boundary.
type Prepared struct {
	before  snapshot
	after   snapshot
	slot    int
	media   *mediaSource
	library string
}

func stale() error {
	return failure("cue_revision_stale", errors.New("saved performance or track identity changed; reload before editing"))
}

// Prepare validates and encodes an edit before Desktop protection, snapshots, and history run.
func Prepare(ctx context.Context, d *sql.DB, library string, edit Edit) (*Prepared, error) {
	if edit.Slot < 1 || edit.Slot > 8 || edit.Revision == "" {
		return nil, failure("cue_invalid", errors.New("select a slot A through H and inspect its current revision"))
	}
	s, err := resolve(ctx, d, library, edit.Identifier)
	if err != nil {
		return nil, err
	}
	if s.revision() != edit.Revision {
		return nil, stale()
	}
	if _, err := s.saved(); err != nil {
		return nil, err
	}
	if !s.Table {
		return nil, failure("saved_performance_unavailable", errors.New("the library has no native performance table"))
	}
	p := &Prepared{before: s, after: s, slot: edit.Slot, library: library}
	if edit.Clear {
		p.after.Cues, err = engine.ClearSavedHotCue(s.Cues, edit.Slot)
	} else {
		err = p.encodeSet(ctx, edit)
	}
	if err != nil {
		var coded *progress.Error
		if errors.As(err, &coded) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, failure("cue_invalid", err)
	}
	p.after.Row = s.Row || len(p.after.Cues) > 0
	if len(p.after.Beat) > 0 {
		p.after.Media = ""
	}
	return p, nil
}

func (p *Prepared) encodeSet(ctx context.Context, edit Edit) error {
	if !finite(edit.Position) || edit.Position < 0 {
		return errors.New("cue position must be finite and nonnegative")
	}
	var rate, limit float64
	var err error
	if len(p.before.Beat) == 0 {
		rate, limit, p.media, err = sampleExtent(ctx, p.library, p.before)
		if err == nil {
			p.after.Beat, err = engine.EncodeUnsetBeatData(rate, limit)
		}
	} else {
		var beat engine.BeatData
		beat, err = engine.DecodeBeatData(p.before.Beat)
		rate, limit = beat.SampleRate, beat.SampleLimit
	}
	if err != nil {
		return err
	}
	if !finite(rate) || rate <= 0 || !finite(limit) || limit <= 0 || edit.Position > limit/rate {
		return errors.New("cue position is outside the track's native extent")
	}
	p.after.Cues, err = engine.SetSavedHotCue(p.after.Beat, p.before.Cues, lib.HotCue{Position: edit.Slot, Offset: edit.Position, Name: edit.Name, Color: edit.Color})
	return err
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// Apply rechecks every captured native interpretation input inside the transaction, then updates only cue bytes
// and, when initially absent, the unset beat-data header needed to interpret their source samples.
func (p *Prepared) Apply(ctx context.Context, d *sql.DB) (*Result, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := load(ctx, tx, p.library, p.before.TrackID)
	if err != nil {
		return nil, err
	}
	if current.revision() != p.before.revision() {
		return nil, stale()
	}
	if p.media != nil {
		if err := p.media.check(); err != nil {
			return nil, err
		}
	}
	changed := !bytes.Equal(p.before.Cues, p.after.Cues) || !bytes.Equal(p.before.Beat, p.after.Beat)
	if changed {
		if p.before.Row {
			_, err = tx.ExecContext(ctx, "UPDATE PerformanceData SET quickCues=?,beatData=? WHERE trackId=?", p.after.Cues, p.after.Beat, p.before.TrackID)
		} else {
			_, err = tx.ExecContext(ctx, "INSERT INTO PerformanceData(trackId,quickCues,beatData) VALUES(?,?,?)", p.before.TrackID, p.after.Cues, p.after.Beat)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Result{TrackID: p.before.TrackID, Slot: p.slot, Revision: p.after.revision(), Changed: changed}, nil
}
