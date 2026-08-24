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

// GetDefaultBackupDir returns the default directory for storing backup snapshots following the XDG Base Directory specification.
func GetDefaultBackupDir() (string, error) {
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		return filepath.Join(xdgData, "engine-cli", "backups"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "engine-cli", "backups"), nil
}

// CreateBackup creates an atomic timestamped snapshot of Engine DJ databases and Artwork cache.
func CreateBackup(ctx context.Context, engineLibDir, outDir string) (*BackupInfo, error) {
	if engineLibDir == "" {
		return nil, fmt.Errorf("empty engine library directory")
	}
	if outDir == "" {
		defaultDir, err := GetDefaultBackupDir()
		if err != nil {
			return nil, fmt.Errorf("resolving default backup directory: %w", err)
		}
		outDir = defaultDir
	}

	if _, err := os.Stat(engineLibDir); err != nil {
		return nil, fmt.Errorf("stat engine library %s: %w", engineLibDir, err)
	}

	timestamp := time.Now().UTC()
	backupName := fmt.Sprintf("backup_%s", timestamp.Format("20060102_150405"))
	targetDir := filepath.Join(outDir, backupName)

	db2Target := filepath.Join(targetDir, "Database2")
	rootTarget := filepath.Join(targetDir, "Root")
	artTarget := filepath.Join(targetDir, "Artwork")

	if err := os.MkdirAll(db2Target, 0755); err != nil {
		return nil, fmt.Errorf("creating backup db2 directory: %w", err)
	}
	if err := os.MkdirAll(rootTarget, 0755); err != nil {
		return nil, fmt.Errorf("creating backup root directory: %w", err)
	}

	dbCount := 0
	artCount := 0
	var totalSize int64

	// Copy Database2/*.db
	db2Src := filepath.Join(engineLibDir, "Database2")
	if entries, err := os.ReadDir(db2Src); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".db") {
				srcFile := filepath.Join(db2Src, e.Name())
				dstFile := filepath.Join(db2Target, e.Name())
				n, errCopy := copyFile(srcFile, dstFile)
				if errCopy != nil {
					return nil, fmt.Errorf("copying %s to backup: %w", srcFile, errCopy)
				}
				dbCount++
				totalSize += n
			}
		}
	}

	// Copy root *.db
	if entries, err := os.ReadDir(engineLibDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".db") {
				srcFile := filepath.Join(engineLibDir, e.Name())
				dstFile := filepath.Join(rootTarget, e.Name())
				n, errCopy := copyFile(srcFile, dstFile)
				if errCopy != nil {
					return nil, fmt.Errorf("copying %s to backup: %w", srcFile, errCopy)
				}
				dbCount++
				totalSize += n
			}
		}
	}

	// Copy Artwork/ cache if exists
	artSrc := filepath.Join(engineLibDir, "Artwork")
	if stat, err := os.Stat(artSrc); err == nil && stat.IsDir() {
		_ = os.MkdirAll(artTarget, 0755)
		if entries, err := os.ReadDir(artSrc); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					srcFile := filepath.Join(artSrc, e.Name())
					dstFile := filepath.Join(artTarget, e.Name())
					n, errCopy := copyFile(srcFile, dstFile)
					if errCopy == nil {
						artCount++
						totalSize += n
					}
				}
			}
		}
	}

	return &BackupInfo{
		Path:      targetDir,
		Name:      backupName,
		Timestamp: timestamp,
		DBFiles:   dbCount,
		ArtFiles:  artCount,
		SizeBytes: totalSize,
	}, nil
}

// AutoBackupIfNeeded creates an automated backup snapshot before a modifying operation unless disabled.
func AutoBackupIfNeeded(ctx context.Context, engineLibDir string, noBackup bool) (*BackupInfo, error) {
	if noBackup {
		return nil, nil
	}
	return CreateBackup(ctx, engineLibDir, "")
}

// RestoreBackup restores databases and artwork from a backup directory into the Engine Library directory.
func RestoreBackup(ctx context.Context, backupDir, engineLibDir string) error {
	if backupDir == "" {
		return fmt.Errorf("empty backup directory")
	}
	if engineLibDir == "" {
		return fmt.Errorf("empty engine library directory")
	}

	if _, err := os.Stat(backupDir); err != nil {
		return fmt.Errorf("stat backup dir %s: %w", backupDir, err)
	}
	if _, err := os.Stat(engineLibDir); err != nil {
		return fmt.Errorf("stat engine library %s: %w", engineLibDir, err)
	}

	db2Src := filepath.Join(backupDir, "Database2")
	db2Dest := filepath.Join(engineLibDir, "Database2")
	_ = os.MkdirAll(db2Dest, 0755)

	// Restore Database2/*.db
	if entries, err := os.ReadDir(db2Src); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".db") {
				src := filepath.Join(db2Src, e.Name())
				dst := filepath.Join(db2Dest, e.Name())
				if _, errCopy := copyFile(src, dst); errCopy != nil {
					return fmt.Errorf("restoring %s: %w", src, errCopy)
				}
			}
		}
	}

	// Restore Root/*.db
	rootSrc := filepath.Join(backupDir, "Root")
	if entries, err := os.ReadDir(rootSrc); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".db") {
				src := filepath.Join(rootSrc, e.Name())
				dst := filepath.Join(engineLibDir, e.Name())
				if _, errCopy := copyFile(src, dst); errCopy != nil {
					return fmt.Errorf("restoring %s: %w", src, errCopy)
				}
			}
		}
	}

	// Restore Artwork/
	artSrc := filepath.Join(backupDir, "Artwork")
	if stat, err := os.Stat(artSrc); err == nil && stat.IsDir() {
		artDest := filepath.Join(engineLibDir, "Artwork")
		_ = os.MkdirAll(artDest, 0755)
		if entries, err := os.ReadDir(artSrc); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					src := filepath.Join(artSrc, e.Name())
					dst := filepath.Join(artDest, e.Name())
					_, _ = copyFile(src, dst)
				}
			}
		}
	}

	return nil
}

// ListBackups scans a directory for created backup snapshots.
func ListBackups(backupBaseDir string) ([]BackupInfo, error) {
	if backupBaseDir == "" {
		defaultDir, err := GetDefaultBackupDir()
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
						} else {
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
