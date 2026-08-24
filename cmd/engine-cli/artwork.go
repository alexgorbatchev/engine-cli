package main

import (
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/artwork"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/spf13/cobra"
)

func newArtworkCommand() *cobra.Command {
	artCmd := &cobra.Command{
		Use:   "artwork",
		Short: "Manage and fix album cover artwork",
		Long: `Manage and fix album cover artwork for your Engine DJ collection.

  - Use 'engine-cli artwork normalize' to crop wide covers to 1:1 square.
  - Use 'engine-cli artwork prune' to remove unused cover art and reclaim disk space.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	artCmd.AddCommand(newArtworkNormalizeCommand())
	artCmd.AddCommand(newArtworkPruneCommand())

	return artCmd
}

func newArtworkNormalizeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "normalize",
		Short: "Crop album cover art to 1:1 square for DJ jog wheels and player displays",
		Long: `Scan your music collection, extract embedded album cover art, and crop wide or banner images into clean 1:1 square covers so they look crisp on DJ jog wheels and player screens without modifying your audio files.

Automatically saves a safety backup of your library before applying changes unless --no-backup is specified.`,
		Example: `  engine-cli artwork normalize
  engine-cli -l "/Volumes/DJ_USB/Engine Library" artwork normalize
  engine-cli --no-backup artwork normalize`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before artwork normalize: %w", err)
			}
			if bkInfo != nil {
				if agent.IsAgentMode() {
					cmd.Printf("backup_snapshot: %s\n", bkInfo.Path)
				} else {
					cmd.Printf("Created automated backup snapshot at %s\n", bkInfo.Path)
				}
			}

			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			artCacheDir := filepath.Join(libDirFlag, "Artwork")

			if !agent.IsAgentMode() {
				cmd.Println("Normalizing collection artwork to 1:1 square (1400x1400)...")
			}
			report, err := artwork.NormalizeArtwork(cmd.Context(), database, libDirFlag, artCacheDir)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: complete")
				cmd.Printf("total_tracks: %d\n", report.TotalTracks)
				cmd.Printf("normalized: %d\n", report.NormalizedCount)
				cmd.Printf("already_square: %d\n", report.AlreadySquare)
				cmd.Printf("no_art: %d\n", report.NoArtCount)
				cmd.Printf("errors: %d\n", report.ErrorCount)
				return nil
			}

			cmd.Println("Artwork Normalization Complete!")
			cmd.Printf("  Total Tracks:     %d\n", report.TotalTracks)
			cmd.Printf("  Normalized to 1:1:%d\n", report.NormalizedCount)
			cmd.Printf("  Already Square:   %d\n", report.AlreadySquare)
			cmd.Printf("  No Art / Audio:   %d\n", report.NoArtCount)
			if report.ErrorCount > 0 {
				cmd.Printf("  Errors / Skipped: %d\n", report.ErrorCount)
			}

			return nil
		},
	}
}

func newArtworkPruneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Clean up unused cover art and reclaim disk space",
		Long: `Scan your collection to find and delete old, unneeded cover art and cached thumbnail files that are no longer used by any songs in your library.

Automatically saves a safety backup of your library before applying changes unless --no-backup is specified.`,
		Example: `  engine-cli artwork prune
  engine-cli -l "/Volumes/DJ_USB/Engine Library" artwork prune
  engine-cli --no-backup artwork prune`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before artwork prune: %w", err)
			}
			if bkInfo != nil {
				if agent.IsAgentMode() {
					cmd.Printf("backup_snapshot: %s\n", bkInfo.Path)
				} else {
					cmd.Printf("Created automated backup snapshot at %s\n", bkInfo.Path)
				}
			}

			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			artCacheDir := filepath.Join(libDirFlag, "Artwork")

			if !agent.IsAgentMode() {
				cmd.Println("Pruning orphaned album artwork records and stale cache files...")
			}
			report, err := artwork.PruneArtwork(cmd.Context(), database, artCacheDir)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: complete")
				cmd.Printf("deleted_db_rows: %d\n", report.DeletedDBRows)
				cmd.Printf("deleted_disk_files: %d\n", report.DeletedDiskFiles)
				return nil
			}

			cmd.Println("Artwork Prune Complete!")
			cmd.Printf("  Deleted DB Rows:    %d\n", report.DeletedDBRows)
			cmd.Printf("  Deleted Disk Files: %d\n", report.DeletedDiskFiles)

			return nil
		},
	}
}
