package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/dj/engine-cli/internal/db"
)

// PlaylistNode represents a tree node in a playlist/folder hierarchy.
type PlaylistNode struct {
	ID         int64          `json:"id"`
	Title      string         `json:"title"`
	ParentID   int64          `json:"parentId"`
	TrackCount int            `json:"trackCount"`
	Children   []PlaylistNode `json:"children,omitempty"`
}

// PlaylistDetails contains comprehensive metadata and track list for a single playlist.
type PlaylistDetails struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	ParentID      int64      `json:"parentId"`
	TrackCount    int        `json:"trackCount"`
	TotalDuration int64      `json:"totalDurationSeconds"`
	Tracks        []db.Track `json:"tracks"`
}

// GetPlaylistHierarchy constructs the tree of folders and playlists.
func GetPlaylistHierarchy(ctx context.Context, database *sql.DB) ([]PlaylistNode, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	allLists, err := db.GetAllPlaylists(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching playlists: %w", err)
	}

	trackCounts := make(map[int64]int)
	for _, pl := range allLists {
		tracks, errT := db.GetPlaylistTracks(ctx, database, pl.ID)
		if errT == nil {
			trackCounts[pl.ID] = len(tracks)
		}
	}

	// Build parent -> children map
	childrenMap := make(map[int64][]db.Playlist)
	for _, pl := range allLists {
		childrenMap[pl.ParentListID] = append(childrenMap[pl.ParentListID], pl)
	}

	var buildTree func(parentID int64) []PlaylistNode
	buildTree = func(parentID int64) []PlaylistNode {
		var nodes []PlaylistNode
		for _, child := range childrenMap[parentID] {
			node := PlaylistNode{
				ID:         child.ID,
				Title:      child.Title,
				ParentID:   child.ParentListID,
				TrackCount: trackCounts[child.ID],
				Children:   buildTree(child.ID),
			}
			nodes = append(nodes, node)
		}
		return nodes
	}

	return buildTree(0), nil
}

// InspectPlaylist returns detailed information and track list for a playlist.
func InspectPlaylist(ctx context.Context, database *sql.DB, listID int64) (*PlaylistDetails, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	allLists, err := db.GetAllPlaylists(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching playlists: %w", err)
	}

	var target *db.Playlist
	for i := range allLists {
		if allLists[i].ID == listID {
			target = &allLists[i]
			break
		}
	}

	if target == nil {
		return nil, fmt.Errorf("playlist ID %d not found", listID)
	}

	tracks, err := db.GetPlaylistTracks(ctx, database, listID)
	if err != nil {
		return nil, fmt.Errorf("fetching playlist tracks: %w", err)
	}

	var totalDuration int64
	for _, t := range tracks {
		if t.Length.Valid {
			totalDuration += t.Length.Int64
		}
	}

	return &PlaylistDetails{
		ID:            target.ID,
		Title:         target.Title,
		ParentID:      target.ParentListID,
		TrackCount:    len(tracks),
		TotalDuration: totalDuration,
		Tracks:        tracks,
	}, nil
}

// FormatPlaylistTree renders the playlist hierarchy into an ASCII tree string rooted at "Collection".
// Each playlist line is formatted as `<title> [id:<id> tracks:<trackCount>]`.
func FormatPlaylistTree(nodes []PlaylistNode) string {
	if len(nodes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Collection\n")
	formatPlaylistChildren(&sb, nodes, "")
	return sb.String()
}

func formatPlaylistChildren(sb *strings.Builder, children []PlaylistNode, prefix string) {
	for i, child := range children {
		isLast := i == len(children)-1
		branch := "├── "
		nextPrefix := prefix + "│   "
		if isLast {
			branch = "└── "
			nextPrefix = prefix + "    "
		}
		sb.WriteString(fmt.Sprintf("%s%s%s [id:%d tracks:%d]\n", prefix, branch, child.Title, child.ID, child.TrackCount))
		if len(child.Children) > 0 {
			formatPlaylistChildren(sb, child.Children, nextPrefix)
		}
	}
}

// FormatPlaylistBulletList renders the playlist hierarchy into a compact token-conservative bulleted list.
func FormatPlaylistBulletList(nodes []PlaylistNode) string {
	if len(nodes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("- Collection\n")
	formatPlaylistBullets(&sb, nodes, "  ")
	return sb.String()
}

func formatPlaylistBullets(sb *strings.Builder, children []PlaylistNode, indent string) {
	for _, child := range children {
		sb.WriteString(fmt.Sprintf("%s- %s [id:%d tracks:%d]\n", indent, child.Title, child.ID, child.TrackCount))
		if len(child.Children) > 0 {
			formatPlaylistBullets(sb, child.Children, indent+"  ")
		}
	}
}

// FormatTrackLine formats a single playlist track as `<artist> - <title> [id:<id> <duration>]`.
func FormatTrackLine(t db.Track) string {
	title := t.Title.String
	if title == "" {
		title = t.Filename.String
	}
	artist := t.Artist.String

	trackName := title
	if artist != "" {
		trackName = fmt.Sprintf("%s - %s", artist, title)
	}

	var mins, secs int64
	if t.Length.Valid && t.Length.Int64 > 0 {
		mins = t.Length.Int64 / 60
		secs = t.Length.Int64 % 60
	}

	return fmt.Sprintf("%s [id:%d %d:%02d]", trackName, t.ID, mins, secs)
}
