package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/acquisition"
	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type addTrackParams struct {
	Paths      []string `json:"paths"`
	ToPlaylist string   `json:"to_playlist"`
	Move       bool     `json:"move"`
}
type removeTrackParams struct {
	Tracks     []string `json:"tracks"`
	DeleteFile bool     `json:"delete_file"`
}
type moveTrackParams struct {
	Identifier  string `json:"identifier"`
	Destination string `json:"destination"`
}
type relocateParams struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type upgradeParams struct {
	SelectionToken     string  `json:"selection_token"`
	CandidateURL       string  `json:"candidate_url"`
	Sources            string  `json:"sources"`
	SkipVerify         bool    `json:"skip_verify"`
	SkipMetadata       bool    `json:"skip_metadata"`
	NoCache            bool    `json:"no_cache"`
	AutoInstall        bool    `json:"auto_install"`
	MinBandwidthKHz    float64 `json:"min_bandwidth_khz"`
	ConfirmFingerprint *bool   `json:"confirm_fingerprint"`
	JSRuntime          string  `json:"js_runtime"`
	Identifier         string  `json:"identifier"`
	Query              string  `json:"query"`
	KeepTiming         bool    `json:"keep_timing"`
	ClearTiming        bool    `json:"clear_timing"`
}

type metadataParams track.MetadataEdit

func (p metadataParams) validate() error { return track.MetadataEdit(p).Validate() }

func (p upgradeParams) validate() error {
	if p.KeepTiming && p.ClearTiming {
		return fmt.Errorf("choose keep_timing or clear_timing, not both")
	}
	return p.acquisitionOptions().Validate()
}

func (p upgradeParams) acquisitionOptions() acquire.TrackOptions {
	return acquire.TrackOptions{Sources: p.Sources, SkipVerify: p.SkipVerify, SkipMetadata: p.SkipMetadata, NoCache: p.NoCache, AutoInstall: p.AutoInstall, MinBandwidthKHz: p.MinBandwidthKHz, ConfirmFingerprint: p.ConfirmFingerprint, JSRuntime: p.JSRuntime}
}

func (s *Server) trackOperations() []operation {
	return []operation{
		s.suggestionOperation(),
		s.detailOperation("track"),
		define("track.update", true, []string{"identifier"}, func(ctx context.Context, p metadataParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				t, err := track.EditMetadata(ctx, d, track.MetadataEdit(p), s.config.ArtworkRunner)
				if err != nil {
					return nil, err
				}
				return details.OneTrack(ctx, d, t)
			})
		}),
		define("track.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				tracks, err := db.GetAllTracks(ctx, d)
				if err != nil {
					return nil, err
				}
				all := make([]*db.Track, len(tracks))
				for i := range tracks {
					all[i] = &tracks[i]
				}
				return details.Tracks(ctx, d, all...)
			})
		}),
		define("track.add", true, []string{"paths"}, func(ctx context.Context, p addTrackParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				added, err := batch(ctx, "track.add", p.Paths, func(ctx context.Context, path string) (addedTrack, error) {
					r, err := track.AddTrack(ctx, d, s.config.LibraryDir, path, p.ToPlaylist, p.Move)
					return addedTrack{r}, err
				})
				return trackResults(ctx, d, added, err)
			})
		}),
		define("track.rm", true, []string{"tracks"}, func(ctx context.Context, p removeTrackParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				removed, err := batch(ctx, "track.rm", p.Tracks, func(ctx context.Context, id string) (removedTrack, error) {
					t, err := track.RemoveTrack(ctx, d, s.config.LibraryDir, id, p.DeleteFile)
					return removedTrack{t}, err
				})
				return trackResults(ctx, d, removed, err)
			})
		}),
		define("track.move", true, []string{"identifier", "destination"}, func(ctx context.Context, p moveTrackParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				t, err := track.MoveTrackFile(ctx, d, s.config.LibraryDir, p.Identifier, p.Destination)
				if err != nil {
					return nil, err
				}
				return details.OneTrack(ctx, d, t)
			})
		}),
		define("track.relocate", true, []string{"from", "to"}, func(ctx context.Context, p relocateParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				n, err := track.RelocateTracks(ctx, d, p.From, p.To)
				return map[string]int{"updated": n}, err
			})
		}),
		define("track.upgrade", true, []string{"identifier"}, func(ctx context.Context, p upgradeParams) (any, error) {
			options := p.acquisitionOptions()
			if p.SelectionToken != "" || p.CandidateURL != "" {
				if err := s.acquisitionService().ValidateSelection(ctx, p.SelectionToken, "track", acquisition.TrackRequest{Query: p.Query, Options: options}, p.CandidateURL); err != nil {
					return nil, err
				}
				options.CandidateURL = p.CandidateURL
			}
			return s.database(ctx, func(d *sql.DB) (any, error) {
				r, err := track.UpgradeTrack(ctx, d, s.config.LibraryDir, p.Identifier, track.UpgradeOptions{Query: p.Query, KeepTiming: p.KeepTiming, ClearTiming: p.ClearTiming, Acquisition: options, Runner: s.config.ToolRunner})
				if err != nil {
					return nil, err
				}
				return upgradeResult(ctx, d, r)
			})
		}),
		define("track.artwork.normalize", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return artwork.NormalizeArtwork(ctx, d, s.config.LibraryDir, filepath.Join(s.config.LibraryDir, "Artwork"))
			})
		}),
	}
}
