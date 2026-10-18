// Package migrate exports Engine DJ libraries without changing their databases.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	djsync "github.com/dj/engine-cli/internal/migrate/sync"
	"github.com/dj/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

type Options struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type TrackFailure struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

type Report struct {
	Source      string         `json:"source"`
	Target      string         `json:"target"`
	XML         string         `json:"xml"`
	Total       int            `json:"total"`
	Copied      int            `json:"copied"`
	Existing    int            `json:"existing"`
	Failed      int            `json:"failed"`
	Unavailable int            `json:"unavailable"`
	Failures    []TrackFailure `json:"failures,omitempty"`
}

// Export copies local audio and publishes resumable state and Rekordbox XML.
// Its caller owns terminal progress events, including completion and errors.
func Export(ctx context.Context, opts Options) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Source == "" {
		return nil, errors.New("source Engine DJ library is required")
	}
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return nil, fmt.Errorf("resolving source: %w", err)
	}
	if opts.Target == "" {
		opts.Target = "rekordbox"
	}
	target, err := filepath.Abs(opts.Target)
	if err != nil {
		return nil, fmt.Errorf("resolving target: %w", err)
	}
	progress.Stage(ctx, "migrate.read", 1)
	if info, err := os.Stat(filepath.Join(source, "Database2", "m.db")); err != nil {
		return nil, fmt.Errorf("reading source library: %w", err)
	} else if !info.Mode().IsRegular() {
		return nil, errors.New("source Database2/m.db must be a regular database file")
	}
	library, err := engine.Import(source, engine.ImportOptions{ImportOriginalCues: true, ImportOriginalGrids: true})
	if err != nil {
		return nil, fmt.Errorf("reading Engine DJ library: %w", err)
	}
	state, err := djsync.LoadState(filepath.Join(target, ".sync_state.json"))
	if err != nil {
		return nil, err
	}
	remapped, mappings := djsync.RemapLibrary(&library, target)
	if err := djsync.ValidateMappings(mappings); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return nil, err
	}
	report := &Report{Source: source, Target: target, XML: filepath.Join(target, "rekordbox.xml"), Total: len(mappings)}
	included := make(map[int]bool)
	var failures error
	for i, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			failures = errors.Join(failures, err)
			break
		}
		status, err := copyTrack(ctx, mapping, copyProgress(ctx, mapping, i, len(mappings)))
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			state.SetTrackStatus(mapping.SourcePath, djsync.StatusPending, mapping.TargetPath, mapping.Size)
			failures = errors.Join(failures, err)
			break
		}
		state.SetTrackStatus(mapping.SourcePath, status, mapping.TargetPath, mapping.Size)
		switch status {
		case djsync.StatusSynced:
			report.Copied++
			included[mapping.SongID] = true
		case djsync.StatusSkipped:
			report.Existing++
			included[mapping.SongID] = true
		case djsync.StatusUnavailable:
			report.Unavailable++
		case djsync.StatusFailed:
			report.Failed++
			state.SetTrackError(mapping.SourcePath, err)
			report.Failures = append(report.Failures, TrackFailure{Source: mapping.SourcePath, Error: progress.PublicError(err)})
			failures = errors.Join(failures, err)
		}
		if err := state.Save(); err != nil {
			failures = errors.Join(failures, err)
			break
		}
		progress.Item(ctx, "migrate.copy", i+1, len(mappings))
	}
	progress.Stage(ctx, "migrate.xml", 98)
	return report, errors.Join(failures, state.Save(), exportXML(report.XML, remapped, included))
}

func copyTrack(ctx context.Context, mapping djsync.FileMapping, callback djsync.ProgressCallback) (djsync.Status, error) {
	status, err := djsync.InspectTrack(ctx, mapping)
	if err != nil || status != djsync.StatusPending {
		return status, err
	}
	_, skipped, err := djsync.NewCopier().CopyFile(ctx, mapping.SourcePath, mapping.TargetPath, callback)
	if err != nil {
		return djsync.StatusFailed, err
	}
	if skipped {
		return djsync.StatusSkipped, nil
	}
	return djsync.StatusSynced, nil
}

type copyStatus struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Track       int    `json:"track"`
	TotalTracks int    `json:"total_tracks"`
	CopiedBytes int64  `json:"copied_bytes"`
	TotalBytes  int64  `json:"total_bytes"`
}

func copyProgress(ctx context.Context, mapping djsync.FileMapping, index, total int) djsync.ProgressCallback {
	var last time.Time
	return func(copied, size int64) {
		now := time.Now()
		if now.Sub(last) < 100*time.Millisecond && copied != size {
			return
		}
		last = now
		fraction := 0.0
		if size > 0 {
			fraction = float64(copied) / float64(size)
		}
		percent := 10 + 85*(float64(index)+fraction)/float64(total)
		status := copyStatus{Source: mapping.SourcePath, Target: mapping.TargetPath, Track: index + 1, TotalTracks: total, CopiedBytes: copied, TotalBytes: size}
		metadata, _ := json.Marshal(status) // String and integer fields cannot fail JSON encoding.
		progress.Emit(ctx, progress.Event{Phase: "migrate.copy", Percent: percent, Metadata: metadata})
	}
}
