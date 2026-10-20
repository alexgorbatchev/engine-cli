package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type mediaParams struct {
	Target          string  `json:"target"`
	Identifier      string  `json:"identifier"`
	StagingID       string  `json:"staging_id"`
	MinBandwidthKHz float64 `json:"min_bandwidth_khz"`
	AutoInstall     bool    `json:"auto_install"`
}

func (p mediaParams) validate() error {
	count := 0
	for _, value := range []string{p.Target, p.Identifier, p.StagingID} {
		if value != "" {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("choose target, identifier, or staging_id")
	}
	return (acquire.TrackOptions{MinBandwidthKHz: p.MinBandwidthKHz}).Validate()
}

type tagInspectParams struct {
	Identifier  string   `json:"identifier"`
	StagingID   string   `json:"staging_id"`
	Path        string   `json:"path"`
	Identifiers []string `json:"identifiers"`
	StagingIDs  []string `json:"staging_ids"`
	Paths       []string `json:"paths"`
}

func (p tagInspectParams) validate() error {
	return p.selection().validate()
}

func (p tagInspectParams) selection() mediaSelection {
	return mediaSelection{p.Identifier, p.StagingID, p.Path, p.Identifiers, p.StagingIDs, p.Paths}
}

type tagParams struct {
	Identifier  string   `json:"identifier"`
	StagingID   string   `json:"staging_id"`
	Path        string   `json:"path"`
	Identifiers []string `json:"identifiers"`
	StagingIDs  []string `json:"staging_ids"`
	Paths       []string `json:"paths"`
	Artist      *string  `json:"artist"`
	Title       *string  `json:"title"`
	Album       *string  `json:"album"`
	Date        *string  `json:"date"`
	Genre       *string  `json:"genre"`
	Comment     *string  `json:"comment"`
	Artwork     string   `json:"artwork"`
	SourceURL   string   `json:"source_url"`
	Refresh     bool     `json:"refresh"`
	DryRun      bool     `json:"dry_run"`
	NoCache     bool     `json:"no_cache"`
	AutoInstall bool     `json:"auto_install"`
}

type artworkParams struct {
	Identifier  string   `json:"identifier"`
	StagingID   string   `json:"staging_id"`
	Path        string   `json:"path"`
	Identifiers []string `json:"identifiers"`
	StagingIDs  []string `json:"staging_ids"`
	Paths       []string `json:"paths"`
	Artwork     string   `json:"artwork"`
}

func (p artworkParams) validate() error {
	return p.selection().validate()
}

func (p artworkParams) selection() mediaSelection {
	return mediaSelection{p.Identifier, p.StagingID, p.Path, p.Identifiers, p.StagingIDs, p.Paths}
}
func (p artworkParams) operationPaths() []string { return p.selection().rawPaths() }

func (p artworkParams) operationEffect() string {
	return p.selection().effect()
}

func (p tagParams) validate() error {
	return p.selection().validate()
}
func (p tagParams) selection() mediaSelection {
	return mediaSelection{p.Identifier, p.StagingID, p.Path, p.Identifiers, p.StagingIDs, p.Paths}
}
func (p tagParams) operationPaths() []string { return p.selection().rawPaths() }
func (p tagParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return p.selection().effect()
}
func (p tagParams) options() acquire.TagOptions {
	return acquire.TagOptions{Artist: p.Artist, Title: p.Title, Album: p.Album, Date: p.Date, Genre: p.Genre, Comment: p.Comment, Artwork: p.Artwork, SourceURL: p.SourceURL, Refresh: p.Refresh, DryRun: p.DryRun, NoCache: p.NoCache, AutoInstall: p.AutoInstall}
}

func (s *Server) mediaPath(ctx context.Context, p mediaParams) (string, error) {
	if p.Target != "" {
		return p.Target, nil
	}
	if p.StagingID != "" {
		return s.acquisitionService().ResolveStaged(ctx, p.StagingID)
	}
	value, err := s.database(ctx, func(d *sql.DB) (any, error) {
		t, err := db.FindTrack(ctx, d, p.Identifier)
		if err != nil {
			return nil, err
		}
		if t == nil {
			return nil, fmt.Errorf("track not found")
		}
		path := t.PathString()
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.config.LibraryDir, path)
		}
		return path, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}
func (s *Server) mediaOperations() []operation {
	client := acquire.Client{Runner: s.config.ToolRunner}
	ops := []operation{
		defineEffect("staging.retry", "staging", []string{"id"}, func(ctx context.Context, p stagedIDParams) (any, error) {
			return s.acquisitionService().Retry(ctx, p.ID)
		}),
		define("track.verify", false, nil, func(ctx context.Context, p mediaParams) (any, error) {
			path, err := s.mediaPath(ctx, p)
			if err != nil {
				return nil, err
			}
			return client.Verify(ctx, path, acquire.VerifyOptions{MinBandwidthKHz: p.MinBandwidthKHz, AutoInstall: p.AutoInstall})
		}),
		define("track.tags.inspect", false, nil, func(ctx context.Context, p tagInspectParams) (any, error) {
			return s.tagSelection(ctx, p.selection(), "inspect", acquire.TagOptions{})
		}),
	}
	ops = append(ops, defineEffect("track.tags.update", "collection", nil, func(ctx context.Context, p tagParams) (any, error) {
		return s.tagSelection(ctx, p.selection(), "update", p.options())
	}), defineEffect("track.artwork.embed", "collection", []string{"artwork"}, func(ctx context.Context, p artworkParams) (any, error) {
		return s.tagSelection(ctx, p.selection(), "artwork", acquire.TagOptions{Artwork: p.Artwork})
	}))
	return ops
}
