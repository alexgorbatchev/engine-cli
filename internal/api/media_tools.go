package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/acquire"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/track"
)

type mediaParams struct {
	Identifier      string  `json:"identifier"`
	StagingID       string  `json:"staging_id"`
	MinBandwidthKHz float64 `json:"min_bandwidth_khz"`
	AutoInstall     bool    `json:"auto_install"`
}

func (p mediaParams) validate() error {
	if (p.Identifier == "") == (p.StagingID == "") {
		return fmt.Errorf("choose identifier or staging_id")
	}
	return (acquire.TrackOptions{MinBandwidthKHz: p.MinBandwidthKHz}).Validate()
}

type tagInspectParams struct {
	Identifier string `json:"identifier"`
	StagingID  string `json:"staging_id"`
}

func (p tagInspectParams) validate() error {
	return (mediaParams{Identifier: p.Identifier, StagingID: p.StagingID}).validate()
}

type tagParams struct {
	Identifier  string  `json:"identifier"`
	StagingID   string  `json:"staging_id"`
	Artist      *string `json:"artist"`
	Title       *string `json:"title"`
	Album       *string `json:"album"`
	Date        *string `json:"date"`
	Genre       *string `json:"genre"`
	Comment     *string `json:"comment"`
	Artwork     string  `json:"artwork"`
	SourceURL   string  `json:"source_url"`
	Refresh     bool    `json:"refresh"`
	DryRun      bool    `json:"dry_run"`
	NoCache     bool    `json:"no_cache"`
	AutoInstall bool    `json:"auto_install"`
}

type artworkParams struct {
	Identifier string `json:"identifier"`
	StagingID  string `json:"staging_id"`
	Artwork    string `json:"artwork"`
}

func (p artworkParams) validate() error {
	return (mediaParams{Identifier: p.Identifier, StagingID: p.StagingID}).validate()
}

func (p artworkParams) operationEffect() string {
	if p.StagingID != "" {
		return "staging"
	}
	return "collection"
}

func (p tagParams) validate() error {
	return (mediaParams{Identifier: p.Identifier, StagingID: p.StagingID}).validate()
}
func (p tagParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	if p.StagingID != "" {
		return "staging"
	}
	return "collection"
}
func (p tagParams) options() acquire.TagOptions {
	return acquire.TagOptions{Artist: p.Artist, Title: p.Title, Album: p.Album, Date: p.Date, Genre: p.Genre, Comment: p.Comment, Artwork: p.Artwork, SourceURL: p.SourceURL, Refresh: p.Refresh, DryRun: p.DryRun, NoCache: p.NoCache, AutoInstall: p.AutoInstall}
}

func (s *Server) mediaPath(ctx context.Context, p mediaParams) (string, error) {
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
			path, err := s.mediaPath(ctx, mediaParams{Identifier: p.Identifier, StagingID: p.StagingID})
			if err != nil {
				return nil, err
			}
			return client.Tags(ctx, "inspect", path, "", acquire.TagOptions{})
		}),
	}
	ops = append(ops, defineEffect("track.tags.update", "collection", nil, func(ctx context.Context, p tagParams) (any, error) {
		if p.StagingID != "" {
			return s.acquisitionService().TagStaged(ctx, p.StagingID, "update", p.options())
		}
		return s.database(ctx, func(d *sql.DB) (any, error) {
			return track.UpdateFileTags(ctx, d, s.config.LibraryDir, p.Identifier, track.FileTagOptions{Options: p.options(), Runner: s.config.ToolRunner})
		})
	}), defineEffect("track.artwork.embed", "collection", []string{"artwork"}, func(ctx context.Context, p artworkParams) (any, error) {
		options := acquire.TagOptions{Artwork: p.Artwork}
		if p.StagingID != "" {
			return s.acquisitionService().TagStaged(ctx, p.StagingID, "artwork", options)
		}
		return s.database(ctx, func(d *sql.DB) (any, error) {
			return track.UpdateFileTags(ctx, d, s.config.LibraryDir, p.Identifier, track.FileTagOptions{Options: options, ArtworkOnly: true, Runner: s.config.ToolRunner})
		})
	}))
	return ops
}
