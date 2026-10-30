package backup

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// BackupInfo contains metadata about a created backup snapshot.
type BackupInfo struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Timestamp time.Time `json:"timestamp"`
	DBFiles   int       `json:"dbFiles"`
	ArtFiles  int       `json:"artFiles"`
	SizeBytes int64     `json:"sizeBytes"`
}

// GetDefaultBackupDir keeps snapshots beside their source library.
func GetDefaultBackupDir(lib string) (string, error) {
	if lib == "" {
		return "", fmt.Errorf("empty engine library directory")
	}
	abs, err := filepath.Abs(lib)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, ".engine-cli", "backups"), nil
}

// CreateBackup creates an atomic timestamped snapshot of Engine DJ databases and Artwork cache.
func CreateBackup(ctx context.Context, engineLibDir, outDir string) (*BackupInfo, error) {
	return createBackup(ctx, engineLibDir, outDir, true)
}

func createBackup(ctx context.Context, engineLibDir, outDir string, artwork bool) (*BackupInfo, error) {
	progress.Stage(ctx, "backup", 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if engineLibDir == "" {
		return nil, fmt.Errorf("empty engine library directory")
	}
	if outDir == "" {
		defaultDir, err := GetDefaultBackupDir(engineLibDir)
		if err != nil {
			return nil, fmt.Errorf("resolving default backup directory: %w", err)
		}
		outDir = defaultDir
	}

	if _, err := os.Stat(engineLibDir); err != nil {
		return nil, fmt.Errorf("stat engine library %s: %w", engineLibDir, err)
	}

	timestamp := time.Now().UTC()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating backup directory: %w", err)
	}
	// Stage beside the final snapshot so publication is one filesystem rename.
	staging, err := os.MkdirTemp(outDir, ".backup-*")
	if err != nil {
		return nil, fmt.Errorf("creating pending snapshot: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		} // Best-effort cleanup of an incomplete snapshot.
	}()
	backupName := fmt.Sprintf("backup_%s_%s", timestamp.Format("20060102_150405"), strings.TrimPrefix(filepath.Base(staging), ".backup-"))
	targetDir := filepath.Join(outDir, backupName)

	db2Target := filepath.Join(staging, "Database2")
	rootTarget := filepath.Join(staging, "Root")
	artTarget := filepath.Join(staging, "Artwork")

	if err := os.MkdirAll(db2Target, 0755); err != nil {
		return nil, fmt.Errorf("creating backup db2 directory: %w", err)
	}
	if err := os.MkdirAll(rootTarget, 0755); err != nil {
		return nil, fmt.Errorf("creating backup root directory: %w", err)
	}

	dbCount, dbBytes, err := snapshotDirectory(ctx, filepath.Join(engineLibDir, "Database2"), db2Target)
	if err != nil {
		return nil, err
	}
	progress.Stage(ctx, "backup.root", 40)
	rootCount, rootBytes, err := snapshotDirectory(ctx, engineLibDir, rootTarget)
	if err != nil {
		return nil, err
	}
	dbCount += rootCount
	totalSize := dbBytes + rootBytes
	companionCount, companionBytes, err := snapshotCompanion(ctx, engineLibDir, staging)
	if err != nil {
		return nil, err
	}
	dbCount += companionCount
	totalSize += companionBytes
	progress.Stage(ctx, "backup.artwork", 70)
	artCount := 0
	if artwork {
		var artBytes int64
		artCount, artBytes, err = copyArtwork(ctx, filepath.Join(engineLibDir, "Artwork"), artTarget)
		if err != nil {
			return nil, err
		}
		totalSize += artBytes
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, targetDir); err != nil {
		return nil, fmt.Errorf("publishing backup snapshot: %w", err)
	}
	published = true
	progress.Stage(ctx, "backup.saved", 95)

	return &BackupInfo{
		Path:      targetDir,
		Name:      backupName,
		Timestamp: timestamp,
		DBFiles:   dbCount,
		ArtFiles:  artCount,
		SizeBytes: totalSize,
	}, nil
}

func copyArtwork(ctx context.Context, src, dst string) (count int, size int64, err error) {
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("reading artwork: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, 0, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if entry.IsDir() {
			continue
		}
		n, err := copyFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
		if err != nil {
			return 0, 0, fmt.Errorf("copying artwork %s: %w", entry.Name(), err)
		}
		count++
		size += n
	}
	return count, size, nil
}

func snapshotDirectory(ctx context.Context, src, dst string) (count int, size int64, err error) {
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("reading database directory %s: %w", src, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".db") {
			continue
		}
		n, err := snapshotDatabase(ctx, filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
		if err != nil {
			return 0, 0, fmt.Errorf("snapshotting %s: %w", entry.Name(), err)
		}
		count++
		size += n
	}
	return count, size, nil
}

// AutoBackupIfNeeded creates an automated backup snapshot before a modifying operation unless disabled.
func AutoBackupIfNeeded(ctx context.Context, engineLibDir string, noBackup bool) (*BackupInfo, error) {
	if noBackup {
		return nil, nil
	}
	info, err := createBackup(progress.Range(ctx, 0, 10), engineLibDir, "", false)
	if err == nil {
		progress.Stage(ctx, "backup.saved", 10)
	}
	return info, err
}

// ListBackups scans a directory for created backup snapshots.
func ListBackups(lib, backupBaseDir string) ([]BackupInfo, error) {
	if backupBaseDir == "" {
		defaultDir, err := GetDefaultBackupDir(lib)
		if err != nil {
			return nil, fmt.Errorf("resolving default backup directory: %w", err)
		}
		backupBaseDir = defaultDir
	}

	if _, err := os.Stat(backupBaseDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat backup dir %s: %w", backupBaseDir, err)
	}

	entries, err := os.ReadDir(backupBaseDir)
	if err != nil {
		return nil, fmt.Errorf("reading backup dir: %w", err)
	}

	var results []BackupInfo
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "backup_") {
			fullPath := filepath.Join(backupBaseDir, e.Name())
			dbCount := 0
			artCount := 0
			var totalSize int64

			_ = filepath.WalkDir(fullPath, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr == nil && !d.IsDir() {
					info, statErr := d.Info()
					if statErr == nil {
						totalSize += info.Size()
						if strings.HasSuffix(strings.ToLower(d.Name()), ".db") {
							dbCount++
						} else if path != filepath.Join(fullPath, companionFolder, companionManifest) {
							artCount++
						}
					}
				}
				return nil
			})

			info, _ := e.Info()
			ts := time.Now()
			if info != nil {
				ts = info.ModTime()
			}

			results = append(results, BackupInfo{
				Path:      fullPath,
				Name:      e.Name(),
				Timestamp: ts,
				DBFiles:   dbCount,
				ArtFiles:  artCount,
				SizeBytes: totalSize,
			})
		}
	}

	return results, nil
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, in)
	if err != nil {
		return 0, err
	}
	return n, nil
}
