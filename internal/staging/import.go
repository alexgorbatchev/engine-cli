package staging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type ImportOptions struct {
	IDs        []string
	ToPlaylist string
	Runner     enginesync.CommandRunner
}
type Imported struct {
	ID    string    `json:"id"`
	Track *db.Track `json:"track"`
}

// MarshalJSON shares collection track values across CLI and HTTP results.
func (i Imported) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID    string         `json:"id"`
		Track map[string]any `json:"track"`
	}{ID: i.ID, Track: details.Track(i.Track)})
}

type Failure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
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
		result, err := s.importTrack(ctx, d, item, opts)
		if result != nil {
			report.Imported = append(report.Imported, Imported{ID: item.ID, Track: result.Track})
		}
		if err != nil {
			report.Failed = append(report.Failed, Failure{ID: item.ID, Error: err.Error()})
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

func (s *Store) importTrack(ctx context.Context, d *sql.DB, item Item, opts ImportOptions) (*track.AddTrackResult, error) {
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
	out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	_, copyErr := io.Copy(out, source)
	if err := errors.Join(copyErr, out.Sync(), out.Close(), ctx.Err()); err != nil {
		return nil, err
	}
	result, err := track.AddTrack(ctx, d, s.lib, dest, opts.ToPlaylist, false, opts.Runner)
	if err != nil {
		return nil, err
	}
	if err := s.remove(ctx, item); err != nil {
		return result, fmt.Errorf("track imported but staged cleanup failed: %w", err)
	}
	return result, nil
}
