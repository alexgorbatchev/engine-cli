package staging

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type ImportOptions struct {
	IDs        []string
	ToPlaylist string
	Runner     enginesync.CommandRunner
}

// Imported pairs a staged ID with the collection track created from it.
type Imported struct {
	ID      string `json:"id"`
	TrackID int64  `json:"track_id"`
	// Analysis reports the import's optional BPM, key, and beat-grid analysis of this track.
	Analysis *track.AnalyzedTrack `json:"analysis,omitempty"`
}

type Failure struct {
	ID        string `json:"id"`
	Error     string `json:"error"`
	ErrorCode string `json:"error_code,omitempty"`
}
type ImportReport struct {
	// Imported records completed collection changes, even if staged cleanup fails.
	// Such items also appear in Failed and remain available for cleanup retry.
	Imported []Imported `json:"imported"`
	Failed   []Failure  `json:"failed"`
}

// Import keeps the acquisition until file installation and playlist assignment
// both succeed. Callers wrap collection changes in the normal guard/history flow.
func (s *Store) Import(ctx context.Context, d *sql.DB, opts ImportOptions) (*ImportReport, error) {
	if d == nil {
		return nil, fmt.Errorf("collection database is required")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	selected, err := s.selected(ctx, opts.IDs)
	if err != nil {
		return nil, err
	}
	report := &ImportReport{Imported: []Imported{}, Failed: []Failure{}}
	for _, item := range selected {
		if item.Kind == "mix" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		result, err := s.importTrack(ctx, d, item, opts, nil)
		if result != nil {
			report.Imported = append(report.Imported, Imported{ID: item.ID, TrackID: result.Track.ID})
		}
		if err != nil {
			report.Failed = append(report.Failed, Failure{ID: item.ID, Error: err.Error(), ErrorCode: progress.ErrorCode(err)})
			continue
		}
	}
	for _, item := range selected {
		if item.Kind != "mix" {
			continue
		}
		current, err := s.Inspect(ctx, item.ID)
		if err != nil {
			return report, err
		}
		if len(current.Items) == 0 {
			if err := s.remove(ctx, *current); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

func (s *Store) importTrack(ctx context.Context, d *sql.DB, item Item, opts ImportOptions, reviewed *importObservation) (*track.AddTrackResult, error) {
	if item.Status != "ready" {
		return nil, fmt.Errorf("staged track is %s; retry acquisition first", item.Status)
	}
	source, err := s.Open(ctx, item.ID, false)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	root, err := os.OpenRoot(s.lib)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel := filepath.Join("Music", "staging", item.ID, filepath.Base(item.AudioPath))
	if err := root.MkdirAll(filepath.Dir(rel), 0755); err != nil {
		return nil, err
	}
	dest := filepath.Join(s.lib, rel)
	if err := history.Preserve(ctx, dest); err != nil {
		return nil, err
	}
	// Installation stays in the library and never overwrites another staged ID.
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if reviewed != nil {
		// A queue retry must reconcile its collection receipt first. A pathname
		// already present is never evidence that this operation committed.
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	out, err := root.OpenFile(rel, flags, 0600)
	if err != nil {
		return nil, err
	}
	var copyErr error
	if reviewed == nil {
		copyErr = copyLocalBytes(ctx, out, source)
	} else {
		hash := sha256.New()
		copyErr = copyLocalBytes(ctx, io.MultiWriter(out, hash), source)
		if copyErr == nil && hex.EncodeToString(hash.Sum(nil)) != reviewed.ContentHash {
			copyErr = staleImport()
		}
	}
	if err := errors.Join(copyErr, out.Sync(), out.Close(), ctx.Err()); err != nil {
		return nil, err
	}
	result, err := track.AddTrack(ctx, d, s.lib, dest, opts.ToPlaylist, false, opts.Runner)
	if err != nil {
		return nil, err
	}
	if reviewed != nil {
		// The durable intake owner must link its result to History before
		// discarding the immutable staged evidence. Cleanup is a later action.
		return result, nil
	}
	if err := s.remove(ctx, item); err != nil {
		return result, fmt.Errorf("track imported but staged cleanup failed: %w", err)
	}
	return result, nil
}
