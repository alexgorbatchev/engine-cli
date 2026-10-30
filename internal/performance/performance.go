// Package performance reads and edits saved Engine cues without rewriting unrelated performance data.
package performance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

type Cue struct {
	Slot     int     `json:"slot"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

type Loop struct {
	Slot  int     `json:"slot"`
	Name  string  `json:"name"`
	Color string  `json:"color"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type Saved struct {
	TrackID  int64  `json:"track_id"`
	Revision string `json:"revision"`
	Cues     []Cue  `json:"cues"`
	Loops    []Loop `json:"loops"`
}

// reader is shared by the initial database read and the transaction's compare-and-swap check.
type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type snapshot struct {
	TrackID  int64
	Origin   string
	OriginID int64
	Path     string
	Filename string
	Table    bool
	Row      bool
	Beat     []byte
	Cues     []byte
	Loops    []byte
	Track    []byte
	Media    string
}

func failure(code string, err error) error {
	return &progress.Error{Code: code, Cause: err}
}

func load(ctx context.Context, q reader, library string, id int64) (snapshot, error) {
	s := snapshot{TrackID: id}
	err := q.QueryRowContext(ctx, "SELECT originDatabaseUuid,originTrackId,COALESCE(path,''),COALESCE(filename,'') FROM Track WHERE id=?", id).Scan(&s.Origin, &s.OriginID, &s.Path, &s.Filename)
	if errors.Is(err, sql.ErrNoRows) {
		return s, failure("track_not_found", errors.New("the selected track no longer exists"))
	}
	if err != nil {
		return s, err
	}
	if s.Origin == "" || s.OriginID <= 0 {
		return s, failure("cue_track_identity_invalid", errors.New("the selected track has no stable origin identity"))
	}
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='PerformanceData')").Scan(&s.Table); err != nil {
		return s, err
	}
	if s.Table {
		err = q.QueryRowContext(ctx, "SELECT beatData,quickCues,loops,trackData FROM PerformanceData WHERE trackId=?", id).Scan(&s.Beat, &s.Cues, &s.Loops, &s.Track)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return s, err
		}
		s.Row = err == nil
	}
	if len(s.Beat) == 0 {
		if _, _, ok := nativeExtent(s.Track); !ok {
			s.Media = inspectMedia(library, s.Path)
		}
	}
	return s, nil
}

func (s snapshot) revision() string {
	data := binary.BigEndian.AppendUint64(nil, uint64(s.TrackID))
	data = binary.BigEndian.AppendUint64(data, uint64(s.OriginID))
	for _, present := range []bool{s.Table, s.Row} {
		if present {
			data = append(data, 1)
		} else {
			data = append(data, 0)
		}
	}
	for _, value := range [][]byte{[]byte(s.Origin), []byte(s.Path), []byte(s.Filename), s.Beat, s.Cues, s.Loops, s.Track, []byte(s.Media)} {
		// SQL NULL and a present zero-length blob remain different revisions.
		if value == nil {
			data = append(data, 0)
		} else {
			data = append(data, 1)
		}
		data = binary.BigEndian.AppendUint64(data, uint64(len(value)))
		data = append(data, value...)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s snapshot) saved() (*Saved, error) {
	decoded, err := engine.DecodeSavedPerformance(s.Beat, s.Cues, s.Loops)
	if err != nil {
		return nil, failure("invalid_saved_performance", err)
	}
	result := &Saved{TrackID: s.TrackID, Revision: s.revision(), Cues: []Cue{}, Loops: []Loop{}}
	for _, cue := range decoded.HotCues {
		result.Cues = append(result.Cues, Cue{Slot: cue.Position, Name: cue.Name, Color: cue.Color, Position: cue.Offset})
	}
	for _, loop := range decoded.Loops {
		result.Loops = append(result.Loops, Loop{Slot: loop.Position, Name: loop.Name, Color: loop.Color, Start: loop.Start, End: loop.End})
	}
	return result, nil
}

func resolve(ctx context.Context, d *sql.DB, library, selector string) (snapshot, error) {
	track, err := db.FindTrack(ctx, d, selector)
	if err != nil {
		return snapshot{}, err
	}
	if track == nil {
		return snapshot{}, failure("track_not_found", errors.New("the selected track no longer exists"))
	}
	return load(ctx, d, library, track.ID)
}

// Inspect returns saved markers and a revision bound to their native interpretation and track identity.
func Inspect(ctx context.Context, d *sql.DB, library, selector string) (*Saved, error) {
	s, err := resolve(ctx, d, library, selector)
	if err != nil {
		return nil, err
	}
	return s.saved()
}
