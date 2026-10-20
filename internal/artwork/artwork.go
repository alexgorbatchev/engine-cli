package artwork

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
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
	progress.Stage(ctx, "artwork.normalize", 10)
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

	for i, t := range tracks {
		progress.Item(progress.Range(ctx, 10, 65), "artwork.tracks", i, len(tracks))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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

		// Compute SHA-1 hash and Base64 RawURL representation (Engine DJ cache format)
		shaSum := sha1.Sum(squareBytes)
		hashHex := hex.EncodeToString(shaSum[:])
		hashBase64URL := base64.RawURLEncoding.EncodeToString(shaSum[:])

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

		// 6. Update cache files in Artwork directory (Engine DJ uses base64url and hex .jpg)
		if artCacheDir != "" {
			cacheFileBase64 := filepath.Join(artCacheDir, fmt.Sprintf("%s.jpg", hashBase64URL))
			cacheFileHex := filepath.Join(artCacheDir, fmt.Sprintf("%s.jpg", hashHex))
			if err := writeCache(ctx, squareBytes, cacheFileBase64, cacheFileHex); err != nil {
				return nil, err
			}
		}

		if w == h && w >= 1000 {
			report.AlreadySquare++
		} else {
			report.NormalizedCount++
		}
	}

	// 7. Normalize all existing rows in AlbumArt table if they contain non-square BLOBs
	progress.Stage(ctx, "artwork.database", 70)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allArtRows, errArtRows := db.GetAllAlbumArt(ctx, database)
	if errArtRows == nil {
		for i, artRow := range allArtRows {
			progress.Item(progress.Range(ctx, 70, 80), "artwork.database.items", i, len(allArtRows))
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(artRow.AlbumArt) == 0 {
				continue
			}
			tmpArtIn := filepath.Join(tmpDir, fmt.Sprintf("db_art_in_%d.jpg", artRow.ID))
			if errWrite := os.WriteFile(tmpArtIn, artRow.AlbumArt, 0644); errWrite != nil {
				continue
			}
			probeOut, errProbe := runner(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=width,height", "-of", "json", tmpArtIn)
			if errProbe != nil || len(probeOut) == 0 {
				continue
			}
			var pd struct {
				Streams []struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"streams"`
			}
			if json.Unmarshal(probeOut, &pd) == nil && len(pd.Streams) > 0 {
				w := pd.Streams[0].Width
				h := pd.Streams[0].Height
				if w > 0 && h > 0 && w != h {
					// Crop non-square BLOB to 1:1 square
					tmpArtSq := filepath.Join(tmpDir, fmt.Sprintf("db_art_sq_%d.jpg", artRow.ID))
					_, errCrop := runner(ctx, "ffmpeg", "-v", "quiet", "-hide_banner", "-y", "-i", tmpArtIn, "-vf", "crop='min(iw,ih)':'min(iw,ih)',scale=1400:1400", "-q:v", "2", tmpArtSq)
					if errCrop == nil {
						if sqBytes, errRead := os.ReadFile(tmpArtSq); errRead == nil && len(sqBytes) > 0 {
							shaSum := sha1.Sum(sqBytes)
							newHash := hex.EncodeToString(shaSum[:])
							hashBase64URL := base64.RawURLEncoding.EncodeToString(shaSum[:])
							_ = db.UpdateAlbumArtByID(ctx, database, artRow.ID, newHash, sqBytes)
							if artCacheDir != "" {
								cacheFileBase64 := filepath.Join(artCacheDir, fmt.Sprintf("%s.jpg", hashBase64URL))
								cacheFileHex := filepath.Join(artCacheDir, fmt.Sprintf("%s.jpg", newHash))
								if err := writeCache(ctx, sqBytes, cacheFileBase64, cacheFileHex); err != nil {
									return nil, err
								}
							}
							report.NormalizedCount++
						}
					}
				}
			}
		}
	}

	// Clean any remaining non-square files in artCacheDir
	progress.Stage(ctx, "artwork.cache", 85)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if artCacheDir != "" && existsDir(artCacheDir) {
		entries, _ := os.ReadDir(artCacheDir)
		for i, e := range entries {
			progress.Item(progress.Range(ctx, 85, 95), "artwork.cache.items", i, len(entries))
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".jpg") {
				p := filepath.Join(artCacheDir, e.Name())
				probeOut, errProbe := runner(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=width,height", "-of", "json", p)
				if errProbe == nil && len(probeOut) > 0 {
					var pd struct {
						Streams []struct {
							Width  int `json:"width"`
							Height int `json:"height"`
						} `json:"streams"`
					}
					if json.Unmarshal(probeOut, &pd) == nil && len(pd.Streams) > 0 {
						if pd.Streams[0].Width > 0 && pd.Streams[0].Height > 0 && pd.Streams[0].Width != pd.Streams[0].Height {
							// Non-square cached thumbnail: crop in-place
							tmpSquare := filepath.Join(tmpDir, fmt.Sprintf("art_sq_%s", e.Name()))
							_, errCrop := runner(ctx, "ffmpeg", "-v", "quiet", "-hide_banner", "-y", "-i", p, "-vf", "crop='min(iw,ih)':'min(iw,ih)',scale=1400:1400", "-q:v", "2", tmpSquare)
							if errCrop == nil {
								if sqBytes, errR := os.ReadFile(tmpSquare); errR == nil {
									if err := writeCache(ctx, sqBytes, p); err != nil {
										return nil, err
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return report, ctx.Err()
}

// PruneArtwork deletes unreferenced AlbumArt table rows and orphaned disk thumbnails.
func PruneArtwork(ctx context.Context, database *sql.DB, artCacheDir string) (*PruneReport, error) {
	progress.Stage(ctx, "artwork.prune", 10)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
			h := strings.ToLower(t.AlbumArtSourceHash.String)
			referencedHashes[h] = true
			if b, errHex := hex.DecodeString(h); errHex == nil {
				referencedHashes[strings.ToLower(base64.RawURLEncoding.EncodeToString(b))] = true
			}
		}
	}

	// Prune AlbumArt table
	allArt, err := db.GetAllAlbumArt(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("fetching album art: %w", err)
	}

	deletedRows := 0
	for i, art := range allArt {
		progress.Item(progress.Range(ctx, 10, 65), "artwork.prune.database", i, len(allArt))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !referencedArtIDs[art.ID] {
			if errDel := db.DeleteAlbumArt(ctx, database, art.ID); errDel == nil {
				deletedRows++
			}
		} else {
			if art.Hash != "" {
				h := strings.ToLower(art.Hash)
				referencedHashes[h] = true
				if b, errHex := hex.DecodeString(h); errHex == nil {
					referencedHashes[strings.ToLower(base64.RawURLEncoding.EncodeToString(b))] = true
				}
			}
		}
	}

	// Prune disk cache
	progress.Stage(ctx, "artwork.prune.cache", 75)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deletedFiles := 0
	if artCacheDir != "" && existsDir(artCacheDir) {
		entries, errDir := os.ReadDir(artCacheDir)
		if errDir == nil {
			for i, e := range entries {
				progress.Item(progress.Range(ctx, 75, 95), "artwork.prune.cache.items", i, len(entries))
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !e.IsDir() {
					name := e.Name()
					baseName := strings.TrimSuffix(name, filepath.Ext(name))
					if !referencedHashes[strings.ToLower(baseName)] {
						filePath := filepath.Join(artCacheDir, name)
						if err := history.Preserve(ctx, filePath); err != nil {
							return nil, err
						}
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

func writeCache(ctx context.Context, data []byte, paths ...string) error {
	if err := history.Preserve(ctx, paths...); err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
