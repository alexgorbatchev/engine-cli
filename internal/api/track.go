package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/artwork"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/details"
	"github.com/dj/engine-cli/internal/track"
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
	Identifier  string `json:"identifier"`
	Query       string `json:"query"`
	KeepTiming  bool   `json:"keep_timing"`
	ClearTiming bool   `json:"clear_timing"`
}

type metadataParams track.MetadataEdit

func (p metadataParams) validate() error { return track.MetadataEdit(p).Validate() }

func (p upgradeParams) validate() error {
	if p.KeepTiming && p.ClearTiming {
		return fmt.Errorf("choose keep_timing or clear_timing, not both")
	}
	return nil
}

func (s *Server) trackOperations() []operation {
	return []operation{
		s.detailOperation("track"),
		define("track.update", true, []string{"identifier"}, func(ctx context.Context, p metadataParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				t, err := track.EditMetadata(ctx, d, track.MetadataEdit(p))
				return details.Track(t), err
			})
		}),
		define("track.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				tracks, err := db.GetAllTracks(ctx, d)
				if err != nil {
					return nil, err
				}
				result := make([]map[string]any, 0, len(tracks))
				for _, track := range tracks {
					result = append(result, details.Track(&track))
				}
				return result, nil
			})
		}),
		define("track.add", true, []string{"paths"}, func(ctx context.Context, p addTrackParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return batch(ctx, "track.add", p.Paths, func(ctx context.Context, path string) (any, error) {
					r, err := track.AddTrack(ctx, d, s.config.LibraryDir, path, p.ToPlaylist, p.Move)
					return addTrackResult(r), err
				})
			})
		}),
		define("track.rm", true, []string{"tracks"}, func(ctx context.Context, p removeTrackParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return batch(ctx, "track.rm", p.Tracks, func(ctx context.Context, id string) (any, error) {
					t, err := track.RemoveTrack(ctx, d, s.config.LibraryDir, id, p.DeleteFile)
					return details.Track(t), err
				})
			})
		}),
		define("track.move", true, []string{"identifier", "destination"}, func(ctx context.Context, p moveTrackParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				t, err := track.MoveTrackFile(ctx, d, s.config.LibraryDir, p.Identifier, p.Destination)
				return details.Track(t), err
			})
		}),
		define("track.relocate", true, []string{"from", "to"}, func(ctx context.Context, p relocateParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				n, err := track.RelocateTracks(ctx, d, p.From, p.To)
				return map[string]int{"updated": n}, err
			})
		}),
		define("track.upgrade", true, []string{"identifier"}, func(ctx context.Context, p upgradeParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				r, err := track.UpgradeTrack(ctx, d, s.config.LibraryDir, p.Identifier, track.UpgradeOptions{Query: p.Query, KeepTiming: p.KeepTiming, ClearTiming: p.ClearTiming})
				return upgradeResult(r), err
			})
		}),
		define("track.artwork.normalize", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return artwork.NormalizeArtwork(ctx, d, s.config.LibraryDir, filepath.Join(s.config.LibraryDir, "Artwork"))
			})
		}),
	}
}
