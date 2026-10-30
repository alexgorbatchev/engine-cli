package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type restoreSection struct {
	folder, destination, phase string
	percent                    float64
	databases                  bool
	entries                    []os.DirEntry
}

// RestoreBackup restores databases and artwork from a backup directory.
func RestoreBackup(ctx context.Context, backupDir, engineLibDir string) error {
	progress.Stage(ctx, "restore", 10)
	if err := ctx.Err(); err != nil {
		return err
	}
	if backupDir == "" || engineLibDir == "" {
		return fmt.Errorf("backup and library directories are required")
	}
	for _, path := range []string{backupDir, engineLibDir} {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("reading restore directory %s: %w", path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("restore path is not a directory: %s", path)
		}
	}
	// Enumerate every source before changing live files: a later unreadable
	// folder must never turn an incomplete restore into a reported success.
	sections, err := planRestore(backupDir, engineLibDir)
	if err != nil {
		return err
	}
	companionSource, err := planCompanion(ctx, backupDir)
	if err != nil {
		return err
	}
	for _, section := range sections {
		progress.Stage(ctx, section.phase, section.percent)
		for _, entry := range section.entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || (section.databases && !strings.HasSuffix(strings.ToLower(entry.Name()), ".db")) {
				continue
			}
			if err := os.MkdirAll(section.destination, 0755); err != nil {
				return fmt.Errorf("creating restore destination: %w", err)
			}
			source := filepath.Join(backupDir, section.folder, entry.Name())
			destination := filepath.Join(section.destination, entry.Name())
			if !section.databases {
				if err := history.Preserve(ctx, destination); err != nil {
					return err
				}
			}
			if section.databases {
				if err := restoreDatabase(ctx, source, destination); err != nil {
					return fmt.Errorf("restoring %s: %w", source, err)
				}
				continue
			}
			if _, err := copyFile(source, destination); err != nil {
				return fmt.Errorf("restoring %s: %w", source, err)
			}
		}
	}
	if err := restoreCompanion(ctx, engineLibDir, companionSource); err != nil {
		return errors.Join(err, history.RequireRecovery(ctx))
	}
	return nil
}

func planRestore(backupDir, library string) ([]restoreSection, error) {
	sections := []restoreSection{
		{folder: "Database2", destination: filepath.Join(library, "Database2"), phase: "restore", percent: 10, databases: true},
		{folder: "Root", destination: library, phase: "restore.root", percent: 50, databases: true},
		{folder: "Artwork", destination: filepath.Join(library, "Artwork"), phase: "restore.artwork", percent: 75},
	}
	hasDatabase := false
	for i := range sections {
		section := &sections[i]
		entries, err := os.ReadDir(filepath.Join(backupDir, section.folder))
		// Root databases and artwork are optional; some snapshots have no Database2.
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, &progress.Error{Code: "backup_unavailable", Cause: fmt.Errorf("reading backup %s: %w", section.folder, err)}
		}
		section.entries = entries
		for _, entry := range entries {
			if section.databases && !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".db") {
				hasDatabase = true
			}
		}
	}
	if !hasDatabase {
		return nil, &progress.Error{Code: "backup_unavailable", Cause: errors.New("backup contains no databases")}
	}
	return sections, nil
}
