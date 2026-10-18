package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
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

// InspectPlaylist returns detailed information and track list for a playlist (by ID or title).
func InspectPlaylist(ctx context.Context, database *sql.DB, idOrTitle string) (*PlaylistDetails, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	target, err := db.FindPlaylist(ctx, database, idOrTitle)
	if err != nil {
		return nil, fmt.Errorf("finding playlist %q: %w", idOrTitle, err)
	}
	if target == nil {
		return nil, fmt.Errorf("playlist %q not found", idOrTitle)
	}

	tracks, err := db.GetPlaylistTracks(ctx, database, target.ID)
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

// CreatePlaylist creates a new playlist or folder.
func CreatePlaylist(ctx context.Context, database *sql.DB, title string, parentIdentifier string, isFolder bool) (*db.Playlist, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("playlist title cannot be empty")
	}

	var parentID int64
	if strings.TrimSpace(parentIdentifier) != "" {
		parentPL, err := db.FindPlaylist(ctx, database, parentIdentifier)
		if err != nil {
			return nil, fmt.Errorf("resolving parent playlist %q: %w", parentIdentifier, err)
		}
		if parentPL == nil {
			return nil, fmt.Errorf("parent playlist %q not found", parentIdentifier)
		}
		parentID = parentPL.ID
	}

	id, err := db.CreatePlaylist(ctx, database, title, parentID, isFolder)
	if err != nil {
		return nil, err
	}

	return &db.Playlist{
		ID:           id,
		Title:        title,
		ParentListID: parentID,
		IsFolder:     isFolder,
	}, nil
}

// DeletePlaylist deletes a playlist by ID or title.
func DeletePlaylist(ctx context.Context, database *sql.DB, identifier string, force bool) (*db.Playlist, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	target, err := db.FindPlaylist(ctx, database, identifier)
	if err != nil {
		return nil, fmt.Errorf("finding playlist %q: %w", identifier, err)
	}
	if target == nil {
		return nil, fmt.Errorf("playlist %q not found", identifier)
	}

	if err := db.DeletePlaylist(ctx, database, target.ID, force); err != nil {
		return nil, err
	}

	return target, nil
}

// MovePlaylist relocates a playlist to a new parent folder.
func MovePlaylist(ctx context.Context, database *sql.DB, identifier string, toParentIdentifier string) (*db.Playlist, *db.Playlist, error) {
	if database == nil {
		return nil, nil, fmt.Errorf("database cannot be nil")
	}

	target, err := db.FindPlaylist(ctx, database, identifier)
	if err != nil {
		return nil, nil, fmt.Errorf("finding playlist %q: %w", identifier, err)
	}
	if target == nil {
		return nil, nil, fmt.Errorf("playlist %q not found", identifier)
	}

	var parentID int64
	var parentPL *db.Playlist
	if strings.TrimSpace(toParentIdentifier) != "" && toParentIdentifier != "0" && !strings.EqualFold(toParentIdentifier, "Collection") {
		parentPL, err = db.FindPlaylist(ctx, database, toParentIdentifier)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving destination parent %q: %w", toParentIdentifier, err)
		}
		if parentPL == nil {
			return nil, nil, fmt.Errorf("destination parent %q not found", toParentIdentifier)
		}
		parentID = parentPL.ID
	}

	if err := db.MovePlaylist(ctx, database, target.ID, parentID); err != nil {
		return nil, nil, err
	}

	target.ParentListID = parentID
	return target, parentPL, nil
}

// AddTrackToPlaylist resolves playlist and track and links them in PlaylistEntity.
func AddTrackToPlaylist(ctx context.Context, database *sql.DB, playlistIdentifier string, trackIdentifier string) (*db.Playlist, *db.Track, error) {
	if database == nil {
		return nil, nil, fmt.Errorf("database cannot be nil")
	}

	pl, err := db.FindPlaylist(ctx, database, playlistIdentifier)
	if err != nil {
		return nil, nil, fmt.Errorf("finding playlist %q: %w", playlistIdentifier, err)
	}
	if pl == nil {
		return nil, nil, fmt.Errorf("playlist %q not found", playlistIdentifier)
	}

	tr, err := db.FindTrack(ctx, database, trackIdentifier)
	if err != nil {
		return nil, nil, fmt.Errorf("finding track %q: %w", trackIdentifier, err)
	}
	if tr == nil {
		return nil, nil, fmt.Errorf("track %q not found in collection", trackIdentifier)
	}

	if _, err := db.AddTrackToPlaylist(ctx, database, pl.ID, tr.ID); err != nil {
		return nil, nil, err
	}

	return pl, tr, nil
}

// RemoveTrackFromPlaylist resolves playlist and track and unlinks them.
func RemoveTrackFromPlaylist(ctx context.Context, database *sql.DB, playlistIdentifier string, trackIdentifier string) (*db.Playlist, *db.Track, error) {
	if database == nil {
		return nil, nil, fmt.Errorf("database cannot be nil")
	}

	pl, err := db.FindPlaylist(ctx, database, playlistIdentifier)
	if err != nil {
		return nil, nil, fmt.Errorf("finding playlist %q: %w", playlistIdentifier, err)
	}
	if pl == nil {
		return nil, nil, fmt.Errorf("playlist %q not found", playlistIdentifier)
	}

	tr, err := db.FindTrack(ctx, database, trackIdentifier)
	if err != nil {
		return nil, nil, fmt.Errorf("finding track %q: %w", trackIdentifier, err)
	}
	if tr == nil {
		return nil, nil, fmt.Errorf("track %q not found in collection", trackIdentifier)
	}

	if err := db.RemoveTrackFromPlaylist(ctx, database, pl.ID, tr.ID); err != nil {
		return nil, nil, err
	}

	return pl, tr, nil
}

// MoveTrackBetweenPlaylists resolves playlists and track and moves the membership.
func MoveTrackBetweenPlaylists(ctx context.Context, database *sql.DB, srcPlaylistIdentifier string, dstPlaylistIdentifier string, trackIdentifier string) (*db.Playlist, *db.Playlist, *db.Track, error) {
	if database == nil {
		return nil, nil, nil, fmt.Errorf("database cannot be nil")
	}

	srcPL, err := db.FindPlaylist(ctx, database, srcPlaylistIdentifier)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("finding source playlist %q: %w", srcPlaylistIdentifier, err)
	}
	if srcPL == nil {
		return nil, nil, nil, fmt.Errorf("source playlist %q not found", srcPlaylistIdentifier)
	}

	dstPL, err := db.FindPlaylist(ctx, database, dstPlaylistIdentifier)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("finding destination playlist %q: %w", dstPlaylistIdentifier, err)
	}
	if dstPL == nil {
		return nil, nil, nil, fmt.Errorf("destination playlist %q not found", dstPlaylistIdentifier)
	}

	tr, err := db.FindTrack(ctx, database, trackIdentifier)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("finding track %q: %w", trackIdentifier, err)
	}
	if tr == nil {
		return nil, nil, nil, fmt.Errorf("track %q not found in collection", trackIdentifier)
	}

	if err := db.MoveTrackInPlaylist(ctx, database, srcPL.ID, dstPL.ID, tr.ID); err != nil {
		return nil, nil, nil, err
	}

	return srcPL, dstPL, tr, nil
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
