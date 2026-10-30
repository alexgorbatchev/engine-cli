package sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

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

// SyncReport summarizes the library synchronization results.
type SyncReport struct {
	TotalDBTracks  int      `json:"totalDbTracks"`
	UpdatedCount   int      `json:"updatedCount"`
	MissingCount   int      `json:"missingCount"`
	UnindexedCount int      `json:"unindexedCount"`
	UnindexedPaths []string `json:"unindexedPaths,omitempty"`
}

// SyncLibrary synchronizes on-disk audio file tags and exact fileBytes into the SQLite database.
func SyncLibrary(ctx context.Context, database *sql.DB, musicBaseDir string, runnerOpt ...CommandRunner) (*SyncReport, error) {
	progress.Stage(ctx, "sync", 10)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	if musicBaseDir == "" {
		return nil, fmt.Errorf("empty music base directory")
	}

	runner := defaultRunner
	if len(runnerOpt) > 0 && runnerOpt[0] != nil {
		runner = runnerOpt[0]
	}

	tracks, err := db.GetAllTracks(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching tracks: %w", err)
	}

	report := &SyncReport{
		TotalDBTracks: len(tracks),
	}

	indexedPaths := make(map[string]bool)

	for i, t := range tracks {
		progress.Item(progress.Range(ctx, 10, 85), "sync.tracks", i, len(tracks))
		if err := ctx.Err(); err != nil {
			return report, err
		}
		pathStr := t.PathString()
		if pathStr == "" {
			report.MissingCount++
			continue
		}

		fullPath := filepath.Join(musicBaseDir, pathStr)
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				report.MissingCount++
				continue
			}
			return report, refreshFailure(ctx, "library_refresh_stat_failed", statErr)
		}

		relPath, err := filepath.Rel(musicBaseDir, fullPath)
		if err != nil {
			return report, refreshFailure(ctx, "library_refresh_stat_failed", err)
		}
		if relPath != "" {
			indexedPaths[filepath.Clean(relPath)] = true
		}

		// Probe audio tags using ffprobe
		probeOut, errProbe := runner(ctx, "ffprobe",
			"-v", "error",
			"-show_entries", "format_tags=title,artist,album,genre,date,year",
			"-of", "json",
			fullPath,
		)
		if err := ctx.Err(); err != nil {
			return report, err
		}

		hasChanges := false
		if t.FileBytes.Int64 != info.Size() {
			t.FileBytes = sql.NullInt64{Int64: info.Size(), Valid: true}
			hasChanges = true
		}

		if errProbe == nil && len(probeOut) > 0 {
			var pData struct {
				Format struct {
					Tags struct {
						Title  string `json:"title"`
						Artist string `json:"artist"`
						Album  string `json:"album"`
						Genre  string `json:"genre"`
						Date   string `json:"date"`
						Year   string `json:"year"`
					} `json:"tags"`
				} `json:"format"`
			}
			if json.Unmarshal(probeOut, &pData) == nil {
				tags := pData.Format.Tags
				if tags.Title != "" && tags.Title != t.Title.String {
					t.Title = sql.NullString{String: tags.Title, Valid: true}
					hasChanges = true
				}
				if tags.Artist != "" && tags.Artist != t.Artist.String {
					t.Artist = sql.NullString{String: tags.Artist, Valid: true}
					hasChanges = true
				}
				if tags.Album != "" && tags.Album != t.Album.String {
					t.Album = sql.NullString{String: tags.Album, Valid: true}
					hasChanges = true
				}
				if tags.Genre != "" && tags.Genre != t.Genre.String {
					t.Genre = sql.NullString{String: tags.Genre, Valid: true}
					hasChanges = true
				}

				dateStr := tags.Date
				if dateStr == "" {
					dateStr = tags.Year
				}
				if len(dateStr) >= 4 {
					if y, errY := strconv.Atoi(dateStr[:4]); errY == nil && y > 0 {
						if t.Year.Int64 != int64(y) {
							t.Year = sql.NullInt64{Int64: int64(y), Valid: true}
							hasChanges = true
						}
					}
				}
			}
		}

		if hasChanges {
			if err := db.UpdateTrackMetadata(ctx, database, &t); err != nil {
				return report, refreshFailure(ctx, "library_refresh_write_failed", err)
			}
			report.UpdatedCount++
		}
	}

	// Scan musicBaseDir for unindexed audio files
	progress.Stage(ctx, "sync.scan", 90)
	err = filepath.WalkDir(musicBaseDir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".m4a" || ext == ".mp3" || ext == ".opus" || ext == ".flac" || ext == ".wav" {
				rel, errRel := filepath.Rel(musicBaseDir, path)
				if errRel != nil {
					return errRel
				}
				cleanRel := filepath.Clean(rel)
				if !indexedPaths[cleanRel] {
					report.UnindexedCount++
					report.UnindexedPaths = append(report.UnindexedPaths, cleanRel)
				}
			}
		}
		return nil
	})
	if err != nil {
		return report, refreshFailure(ctx, "library_refresh_scan_failed", err)
	}

	return report, ctx.Err()
}

func refreshFailure(ctx context.Context, code string, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &progress.Error{Code: code, Cause: cause}
}
