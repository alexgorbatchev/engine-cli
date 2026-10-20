package verify

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// IssueType categorizes an integrity check failure.
type IssueType string

const (
	IssueMissingOnDisk     IssueType = "missing_on_disk"
	IssueFileBytesMismatch IssueType = "file_bytes_mismatch"
	IssueMissingAlbumArt   IssueType = "missing_album_art"
	IssueOrphanedEntity    IssueType = "orphaned_entity"
	IssueEmptyPlaylist     IssueType = "empty_playlist"
)

// Issue represents a single detected integrity problem.
type Issue struct {
	Type        IssueType `json:"type"`
	TrackID     int64     `json:"trackId,omitempty"`
	PlaylistID  int64     `json:"playlistId,omitempty"`
	Path        string    `json:"path,omitempty"`
	Description string    `json:"description"`
}

// VerifyReport summarizes the integrity health of an Engine DJ collection.
type VerifyReport struct {
	TotalTracks        int     `json:"totalTracks"`
	TotalPlaylists     int     `json:"totalPlaylists"`
	MissingOnDiskCount int     `json:"missingOnDiskCount"`
	BytesMismatchCount int     `json:"bytesMismatchCount"`
	MissingArtCount    int     `json:"missingArtCount"`
	OrphanedCount      int     `json:"orphanedCount"`
	EmptyPlaylistCount int     `json:"emptyPlaylistCount"`
	Issues             []Issue `json:"issues"`
	IsHealthy          bool    `json:"isHealthy"`
}

// VerifyLibrary performs a comprehensive integrity check on an Engine DJ database and files.
func VerifyLibrary(ctx context.Context, database *sql.DB, musicBaseDir string) (*VerifyReport, error) {
	progress.Stage(ctx, "verify", 10)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	if musicBaseDir == "" {
		return nil, fmt.Errorf("empty music base directory")
	}

	tracks, err := db.GetAllTracks(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching tracks for verification: %w", err)
	}

	playlists, err := db.GetAllPlaylists(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching playlists for verification: %w", err)
	}

	albumArtRecords, err := db.GetAllAlbumArt(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching album art for verification: %w", err)
	}

	artMap := make(map[int64]bool)
	for _, a := range albumArtRecords {
		artMap[a.ID] = true
	}

	var issues []Issue
	missingDisk := 0
	bytesMismatch := 0
	missingArt := 0

	// Check tracks
	for i, t := range tracks {
		progress.Item(progress.Range(ctx, 10, 65), "verify.tracks", i, len(tracks))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pathStr := t.PathString()
		fullPath := filepath.Join(musicBaseDir, pathStr)
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			missingDisk++
			issues = append(issues, Issue{
				Type:        IssueMissingOnDisk,
				TrackID:     t.ID,
				Path:        pathStr,
				Description: fmt.Sprintf("Track ID %d not found on disk at %s", t.ID, fullPath),
			})
		} else {
			// Check fileBytes mismatch
			if t.FileBytes.Valid && t.FileBytes.Int64 > 0 && t.FileBytes.Int64 != info.Size() {
				bytesMismatch++
				issues = append(issues, Issue{
					Type:        IssueFileBytesMismatch,
					TrackID:     t.ID,
					Path:        pathStr,
					Description: fmt.Sprintf("Track ID %d fileBytes mismatch (DB: %d, Disk: %d bytes)", t.ID, t.FileBytes.Int64, info.Size()),
				})
			}
		}

		// Check albumArtId
		if t.AlbumArtID.Valid && t.AlbumArtID.Int64 > 0 {
			if !artMap[t.AlbumArtID.Int64] {
				missingArt++
				issues = append(issues, Issue{
					Type:        IssueMissingAlbumArt,
					TrackID:     t.ID,
					Description: fmt.Sprintf("Track ID %d references missing AlbumArt ID %d", t.ID, t.AlbumArtID.Int64),
				})
			}
		}
	}

	// Check orphaned playlist entities
	progress.Stage(ctx, "verify.memberships", 70)
	orphanedCount := 0
	trackIDMap := make(map[int64]bool)
	for _, t := range tracks {
		trackIDMap[t.ID] = true
	}

	playlistRows, err := database.QueryContext(ctx, "SELECT id, listId, trackId FROM PlaylistEntity;")
	if err == nil {
		defer playlistRows.Close()
		for playlistRows.Next() {
			var peID, listID, trackID int64
			if errScan := playlistRows.Scan(&peID, &listID, &trackID); errScan == nil {
				if !trackIDMap[trackID] {
					orphanedCount++
					issues = append(issues, Issue{
						Type:        IssueOrphanedEntity,
						PlaylistID:  listID,
						TrackID:     trackID,
						Description: fmt.Sprintf("PlaylistEntity %d in playlist %d references non-existent track ID %d", peID, listID, trackID),
					})
				}
			}
		}
	}

	// Check empty playlists
	emptyPlaylistCount := 0
	for i, p := range playlists {
		progress.Item(progress.Range(ctx, 75, 95), "verify.playlists", i, len(playlists))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plTracks, errPL := db.GetPlaylistTracks(ctx, database, p.ID)
		if errPL == nil && len(plTracks) == 0 {
			emptyPlaylistCount++
			issues = append(issues, Issue{
				Type:        IssueEmptyPlaylist,
				PlaylistID:  p.ID,
				Description: fmt.Sprintf("Playlist %q (ID %d) contains 0 tracks", p.Title, p.ID),
			})
		}
	}

	report := &VerifyReport{
		TotalTracks:        len(tracks),
		TotalPlaylists:     len(playlists),
		MissingOnDiskCount: missingDisk,
		BytesMismatchCount: bytesMismatch,
		MissingArtCount:    missingArt,
		OrphanedCount:      orphanedCount,
		EmptyPlaylistCount: emptyPlaylistCount,
		Issues:             issues,
		IsHealthy:          len(issues) == 0,
	}

	return report, ctx.Err()
}
