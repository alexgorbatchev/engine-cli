package artwork

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dj/engine-cli/internal/db"
)

// CommandRunner abstracts external command execution for testability.
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

// NormalizeReport summarizes the artwork normalization run.
type NormalizeReport struct {
	TotalTracks     int `json:"totalTracks"`
	NormalizedCount int `json:"normalizedCount"`
	AlreadySquare   int `json:"alreadySquare"`
	NoArtCount      int `json:"noArtCount"`
	ErrorCount      int `json:"errorCount"`
}

// PruneReport summarizes the artwork pruning run.
type PruneReport struct {
	DeletedDBRows    int `json:"deletedDbRows"`
	DeletedDiskFiles int `json:"deletedDiskFiles"`
}

// NormalizeArtwork inspects all tracks, extracts embedded artwork, normalizes non-square images to 1:1 square,
// and synchronizes AlbumArt database rows and disk cache.
func NormalizeArtwork(ctx context.Context, database *sql.DB, musicBaseDir, artCacheDir string, runnerOpt ...CommandRunner) (*NormalizeReport, error) {
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

	tmpDir, err := os.MkdirTemp("", "engine_art_*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if artCacheDir != "" {
		_ = os.MkdirAll(artCacheDir, 0755)
	}

	report := &NormalizeReport{
		TotalTracks: len(tracks),
	}

	for _, t := range tracks {
		pathStr := t.PathString()
		if pathStr == "" {
			report.NoArtCount++
			continue
		}

		fullPath := filepath.Join(musicBaseDir, pathStr)
		if _, statErr := os.Stat(fullPath); statErr != nil {
			report.ErrorCount++
			continue
		}

		// 1. Probe for attached picture stream
		probeOut, errProbe := runner(ctx, "ffprobe",
			"-v", "error",
			"-select_streams", "v:0",
			"-show_entries", "stream=width,height,codec_name",
			"-of", "json",
			fullPath,
		)
		if errProbe != nil || len(probeOut) == 0 {
			report.NoArtCount++
			continue
		}

		var probeData struct {
			Streams []struct {
				Width     int    `json:"width"`
				Height    int    `json:"height"`
				CodecName string `json:"codec_name"`
			} `json:"streams"`
		}
		if json.Unmarshal(probeOut, &probeData) != nil || len(probeData.Streams) == 0 {
			report.NoArtCount++
			continue
		}

		w := probeData.Streams[0].Width
		h := probeData.Streams[0].Height
		if w == 0 || h == 0 {
			report.NoArtCount++
			continue
		}

		// 2. Extract embedded image
		rawImgPath := filepath.Join(tmpDir, fmt.Sprintf("raw_%d.jpg", t.ID))
		_, errExtract := runner(ctx, "ffmpeg",
			"-v", "quiet",
			"-hide_banner",
			"-y",
			"-i", fullPath,
			"-an",
			"-vcodec", "copy",
			rawImgPath,
		)
		if errExtract != nil {
			// Fallback: transcode video frame to JPEG
			_, errExtract = runner(ctx, "ffmpeg",
				"-v", "quiet",
				"-hide_banner",
				"-y",
				"-i", fullPath,
				"-an",
				"-vframes", "1",
				rawImgPath,
			)
			if errExtract != nil {
				report.ErrorCount++
				continue
			}
		}

		// 3. Normalize to 1:1 square (1400x1400 for high-res / 256x256 thumbnail BLOB)
		squareImgPath := filepath.Join(tmpDir, fmt.Sprintf("square_%d.jpg", t.ID))
		_, errCrop := runner(ctx, "ffmpeg",
			"-v", "quiet",
			"-hide_banner",
			"-y",
			"-i", rawImgPath,
			"-vf", "crop='min(iw,ih)':'min(iw,ih)',scale=1400:1400",
			"-q:v", "2",
			squareImgPath,
		)
		if errCrop != nil {
			report.ErrorCount++
			continue
		}

		squareBytes, errRead := os.ReadFile(squareImgPath)
		if errRead != nil || len(squareBytes) == 0 {
			report.ErrorCount++
			continue
		}

		// Compute SHA-1 hash
		hasher := sha1.New()
		hasher.Write(squareBytes)
		hashHex := hex.EncodeToString(hasher.Sum(nil))

		// 4. Update AlbumArt table
		artID, errArt := db.InsertOrUpdateAlbumArt(ctx, database, hashHex, squareBytes)
		if errArt != nil {
			report.ErrorCount++
			continue
		}

		// 5. Update Track record in database
		t.AlbumArtID = sql.NullInt64{Int64: artID, Valid: true}
		t.AlbumArt = sql.NullString{String: fmt.Sprintf("image://planck/%d", artID), Valid: true}
		t.AlbumArtSourceHash = sql.NullString{String: hashHex, Valid: true}

		if errUpdate := db.UpdateTrackMetadata(ctx, database, &t); errUpdate != nil {
			report.ErrorCount++
			continue
		}

		// 6. Update cache file in Artwork directory if provided
		if artCacheDir != "" {
			cacheFile := filepath.Join(artCacheDir, fmt.Sprintf("%s.jpg", hashHex))
			_ = os.WriteFile(cacheFile, squareBytes, 0644)
		}

		if w == h && w >= 1000 {
			report.AlreadySquare++
		} else {
			report.NormalizedCount++
		}
	}

	return report, nil
}

// PruneArtwork deletes unreferenced AlbumArt table rows and orphaned disk thumbnails.
func PruneArtwork(ctx context.Context, database *sql.DB, artCacheDir string) (*PruneReport, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	tracks, err := db.GetAllTracks(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching tracks: %w", err)
	}

	referencedArtIDs := make(map[int64]bool)
	referencedHashes := make(map[string]bool)

	for _, t := range tracks {
		if t.AlbumArtID.Valid && t.AlbumArtID.Int64 > 0 {
			referencedArtIDs[t.AlbumArtID.Int64] = true
		}
		if t.AlbumArtSourceHash.Valid && t.AlbumArtSourceHash.String != "" {
			referencedHashes[strings.ToLower(t.AlbumArtSourceHash.String)] = true
		}
	}

	// Prune AlbumArt table
	allArt, err := db.GetAllAlbumArt(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching album art: %w", err)
	}

	deletedRows := 0
	for _, art := range allArt {
		if !referencedArtIDs[art.ID] {
			if errDel := db.DeleteAlbumArt(ctx, database, art.ID); errDel == nil {
				deletedRows++
			}
		} else {
			if art.Hash != "" {
				referencedHashes[strings.ToLower(art.Hash)] = true
			}
		}
	}

	// Prune disk cache
	deletedFiles := 0
	if artCacheDir != "" && existsDir(artCacheDir) {
		entries, errDir := os.ReadDir(artCacheDir)
		if errDir == nil {
			for _, e := range entries {
				if !e.IsDir() {
					name := e.Name()
					baseName := strings.TrimSuffix(name, filepath.Ext(name))
					if !referencedHashes[strings.ToLower(baseName)] {
						filePath := filepath.Join(artCacheDir, name)
						if errRem := os.Remove(filePath); errRem == nil {
							deletedFiles++
						}
					}
				}
			}
		}
	}

	return &PruneReport{
		DeletedDBRows:    deletedRows,
		DeletedDiskFiles: deletedFiles,
	}, nil
}

func existsDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
