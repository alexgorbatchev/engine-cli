package track

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/history"
	"github.com/dj/engine-cli/internal/sync"
)

// AddTrackResult contains the result of importing an audio file into the collection.
type AddTrackResult struct {
	Track    *db.Track    `json:"track"`
	Playlist *db.Playlist `json:"playlist,omitempty"`
	Moved    bool         `json:"moved"`
}

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// AddTrack imports an audio file into the Engine DJ collection.
func AddTrack(
	ctx context.Context,
	database *sql.DB,
	libDir string,
	filePath string,
	toPlaylist string,
	moveFile bool,
	runnerOpt ...sync.CommandRunner,
) (*AddTrackResult, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	if filePath == "" {
		return nil, fmt.Errorf("file path cannot be empty")
	}

	runner := defaultRunner
	if len(runnerOpt) > 0 && runnerOpt[0] != nil {
		runner = runnerOpt[0]
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("accessing file %q: %w", filePath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory, expected an audio file", filePath)
	}

	actualPath := filePath
	if moveFile {
		targetDir := filepath.Join(libDir, "Music")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating Music folder in library: %w", err)
		}
		dest := filepath.Join(targetDir, filepath.Base(filePath))
		if filepath.Clean(filePath) != filepath.Clean(dest) {
			if err := history.MoveFile(ctx, filePath, dest); err != nil {
				return nil, fmt.Errorf("moving file to %q: %w", dest, err)
			}
			actualPath = dest
		}
	}

	// Calculate stored path relative to libDir if within libDir
	storedPath := actualPath
	if rel, err := filepath.Rel(libDir, actualPath); err == nil && !strings.HasPrefix(rel, "..") {
		storedPath = rel
	}
	filename := filepath.Base(actualPath)
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(actualPath)), ".")

	// Probe audio metadata using ffprobe
	probeOut, errProbe := runner(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration,bit_rate:format_tags=title,artist,album,genre,date,year,bpm,TBPM",
		"-of", "json",
		actualPath,
	)

	t := &db.Track{
		Path:        sql.NullString{String: storedPath, Valid: true},
		Filename:    sql.NullString{String: filename, Valid: true},
		FileType:    sql.NullString{String: ext, Valid: ext != ""},
		FileBytes:   sql.NullInt64{Int64: info.Size(), Valid: true},
		IsAvailable: sql.NullBool{Bool: true, Valid: true},
		IsAnalyzed:  sql.NullBool{Bool: false, Valid: true},
	}

	if errProbe == nil && len(probeOut) > 0 {
		var pData struct {
			Format struct {
				Duration string `json:"duration"`
				BitRate  string `json:"bit_rate"`
				Tags     struct {
					Title  string `json:"title"`
					Artist string `json:"artist"`
					Album  string `json:"album"`
					Genre  string `json:"genre"`
					Date   string `json:"date"`
					Year   string `json:"year"`
					BPM    string `json:"bpm"`
					TBPM   string `json:"TBPM"`
				} `json:"tags"`
			} `json:"format"`
		}
		if json.Unmarshal(probeOut, &pData) == nil {
			if pData.Format.Tags.Title != "" {
				t.Title = sql.NullString{String: pData.Format.Tags.Title, Valid: true}
			} else {
				t.Title = sql.NullString{String: strings.TrimSuffix(filename, filepath.Ext(filename)), Valid: true}
			}
			if pData.Format.Tags.Artist != "" {
				t.Artist = sql.NullString{String: pData.Format.Tags.Artist, Valid: true}
			}
			if pData.Format.Tags.Album != "" {
				t.Album = sql.NullString{String: pData.Format.Tags.Album, Valid: true}
			}
			if pData.Format.Tags.Genre != "" {
				t.Genre = sql.NullString{String: pData.Format.Tags.Genre, Valid: true}
			}

			dateStr := pData.Format.Tags.Date
			if dateStr == "" {
				dateStr = pData.Format.Tags.Year
			}
			if len(dateStr) >= 4 {
				if y, errY := strconv.Atoi(dateStr[:4]); errY == nil && y > 0 {
					t.Year = sql.NullInt64{Int64: int64(y), Valid: true}
				}
			}

			bpmStr := pData.Format.Tags.BPM
			if bpmStr == "" {
				bpmStr = pData.Format.Tags.TBPM
			}
			if bpmStr != "" {
				if bpmVal, errB := strconv.ParseFloat(bpmStr, 64); errB == nil && bpmVal > 0 {
					t.BPM = sql.NullInt64{Int64: int64(bpmVal), Valid: true}
					t.BPMAnalyzed = sql.NullFloat64{Float64: bpmVal, Valid: true}
				}
			}

			if pData.Format.Duration != "" {
				if durSecs, errD := strconv.ParseFloat(pData.Format.Duration, 64); errD == nil && durSecs > 0 {
					t.Length = sql.NullInt64{Int64: int64(durSecs), Valid: true}
				}
			}

			if pData.Format.BitRate != "" {
				if br, errBr := strconv.ParseInt(pData.Format.BitRate, 10, 64); errBr == nil && br > 0 {
					t.Bitrate = sql.NullInt64{Int64: br / 1000, Valid: true}
				}
			}
		}
	} else {
		t.Title = sql.NullString{String: strings.TrimSuffix(filename, filepath.Ext(filename)), Valid: true}
	}

	// Check if track already exists in database
	existing, err := db.FindTrack(ctx, database, storedPath)
	if err != nil {
		return nil, fmt.Errorf("checking existing track: %w", err)
	}

	if existing != nil {
		t.ID = existing.ID
		if err := db.UpdateTrackMetadata(ctx, database, t); err != nil {
			return nil, fmt.Errorf("updating track: %w", err)
		}
	} else {
		id, err := db.InsertTrack(ctx, database, t)
		if err != nil {
			return nil, fmt.Errorf("inserting track: %w", err)
		}
		t.ID = id
	}

	result := &AddTrackResult{
		Track: t,
		Moved: moveFile,
	}

	// Link to playlist if specified
	if strings.TrimSpace(toPlaylist) != "" {
		pl, err := db.FindPlaylist(ctx, database, toPlaylist)
		if err != nil {
			return nil, fmt.Errorf("finding playlist %q: %w", toPlaylist, err)
		}
		if pl == nil {
			plID, err := db.CreatePlaylist(ctx, database, toPlaylist, 0, false)
			if err != nil {
				return nil, fmt.Errorf("creating playlist %q: %w", toPlaylist, err)
			}
			pl = &db.Playlist{ID: plID, Title: toPlaylist}
		}

		if _, err := db.AddTrackToPlaylist(ctx, database, pl.ID, t.ID); err != nil {
			return nil, fmt.Errorf("adding track to playlist: %w", err)
		}
		result.Playlist = pl
	}

	return result, nil
}

// RemoveTrack removes a track from the database and optionally deletes its file from disk.
func RemoveTrack(ctx context.Context, database *sql.DB, libDir string, identifier string, deleteFile bool) (*db.Track, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	t, err := db.FindTrack(ctx, database, identifier)
	if err != nil {
		return nil, fmt.Errorf("finding track %q: %w", identifier, err)
	}
	if t == nil {
		return nil, fmt.Errorf("track %q not found in collection", identifier)
	}

	if deleteFile {
		pathStr := t.PathString()
		if pathStr != "" {
			fullPath := pathStr
			if !filepath.IsAbs(fullPath) {
				fullPath = filepath.Join(libDir, fullPath)
			}
			if err := history.Preserve(ctx, fullPath); err != nil {
				return nil, err
			}
			if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("deleting audio file %q: %w", fullPath, err)
			}
		}
	}

	if err := db.DeleteTrack(ctx, database, t.ID); err != nil {
		return nil, err
	}

	return t, nil
}

// MoveTrackFile moves an audio file on disk and updates its path in the database.
func MoveTrackFile(ctx context.Context, database *sql.DB, libDir string, identifier string, destPath string) (*db.Track, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	if destPath == "" {
		return nil, fmt.Errorf("destination path cannot be empty")
	}

	t, err := db.FindTrack(ctx, database, identifier)
	if err != nil {
		return nil, fmt.Errorf("finding track %q: %w", identifier, err)
	}
	if t == nil {
		return nil, fmt.Errorf("track %q not found in collection", identifier)
	}

	sourcePath := t.PathString()
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(libDir, sourcePath)
	}

	targetPath := destPath
	destInfo, errStat := os.Stat(destPath)
	if errStat == nil && destInfo.IsDir() {
		targetPath = filepath.Join(destPath, filepath.Base(sourcePath))
	} else if strings.HasSuffix(destPath, "/") || strings.HasSuffix(destPath, "\\") {
		targetPath = filepath.Join(destPath, filepath.Base(sourcePath))
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating destination folder: %w", err)
	}

	if filepath.Clean(sourcePath) != filepath.Clean(targetPath) {
		if err := history.MoveFile(ctx, sourcePath, targetPath); err != nil {
			return nil, fmt.Errorf("moving audio file: %w", err)
		}
	}

	storedPath := targetPath
	if rel, err := filepath.Rel(libDir, targetPath); err == nil && !strings.HasPrefix(rel, "..") {
		storedPath = rel
	}

	t.Path = sql.NullString{String: storedPath, Valid: true}
	t.Filename = sql.NullString{String: filepath.Base(targetPath), Valid: true}

	if info, err := os.Stat(targetPath); err == nil {
		t.FileBytes = sql.NullInt64{Int64: info.Size(), Valid: true}
	}

	if err := db.UpdateTrackMetadata(ctx, database, t); err != nil {
		return nil, fmt.Errorf("updating database path: %w", err)
	}

	return t, nil
}

// RelocateTracks batch replaces directory prefixes for tracks in the database.
func RelocateTracks(ctx context.Context, database *sql.DB, oldPrefix string, newPrefix string) (int, error) {
	return db.RelocateTracks(ctx, database, oldPrefix, newPrefix)
}
