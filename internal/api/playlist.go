package api

import (
	"context"
	"database/sql"
	"github.com/dj/engine-cli/internal/details"

	"github.com/dj/engine-cli/internal/playlist"
)

type createPlaylistParams struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
	Folder bool   `json:"folder"`
}
type removePlaylistParams struct {
	Identifier string `json:"identifier"`
	Force      bool   `json:"force"`
}
type movePlaylistParams struct {
	Identifier string `json:"identifier"`
	To         string `json:"to"`
}
type renamePlaylistParams struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
}
type membershipParams struct {
	Playlist string   `json:"playlist"`
	Tracks   []string `json:"tracks"`
}
type moveMembershipParams struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Tracks []string `json:"tracks"`
}

func (s *Server) playlistOperations() []operation {
	return []operation{
		define("playlist.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return playlist.GetPlaylistHierarchy(ctx, d) })
		}),
		s.detailOperation("playlist"),
		define("playlist.rename", true, []string{"identifier", "title"}, func(ctx context.Context, p renamePlaylistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return playlist.RenamePlaylist(ctx, d, p.Identifier, p.Title) })
		}),
		define("playlist.create", true, []string{"name"}, func(ctx context.Context, p createPlaylistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return playlist.CreatePlaylist(ctx, d, p.Name, p.Parent, p.Folder) })
		}),
		define("playlist.rm", true, []string{"identifier"}, func(ctx context.Context, p removePlaylistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return playlist.DeletePlaylist(ctx, d, p.Identifier, p.Force) })
		}),
		define("playlist.move", true, []string{"identifier"}, func(ctx context.Context, p movePlaylistParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				pl, parent, err := playlist.MovePlaylist(ctx, d, p.Identifier, p.To)
				return map[string]any{"playlist": pl, "parent": parent}, err
			})
		}),
		s.membershipOperation("playlist.track.add"),
		s.membershipOperation("playlist.track.rm"),
		define("playlist.track.move", true, []string{"from", "to", "tracks"}, func(ctx context.Context, p moveMembershipParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return batch(ctx, "playlist.track.move", p.Tracks, func(ctx context.Context, id string) (any, error) {
					from, to, tr, err := playlist.MoveTrackBetweenPlaylists(ctx, d, p.From, p.To, id)
					return map[string]any{"from": from, "to": to, "track": details.Track(tr)}, err
				})
			})
		}),
	}
}

func (s *Server) membershipOperation(name string) operation {
	return define(name, true, []string{"playlist", "tracks"}, func(ctx context.Context, p membershipParams) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			return batch(ctx, name, p.Tracks, func(ctx context.Context, id string) (any, error) {
				run := playlist.AddTrackToPlaylist
				if name == "playlist.track.rm" {
					run = playlist.RemoveTrackFromPlaylist
				}
				pl, tr, err := run(ctx, d, p.Playlist, id)
				return map[string]any{"playlist": pl, "track": details.Track(tr)}, err
			})
		})
	})
}
