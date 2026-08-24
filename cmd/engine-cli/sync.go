package main

import (
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Update song information in Engine DJ from audio files on disk",
		Long: `Scan your audio files and update song titles, artists, BPM, and file sizes in your Engine DJ collection if you modified them outside Engine DJ. Also finds and lists any new songs on disk that are not yet added to your collection.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.`,
		Example: `  engine-cli sync
  engine-cli -l "/Volumes/DJ_USB/Engine Library" sync
  engine-cli --no-backup sync`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before sync: %w", err)
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

			if !agent.IsAgentMode() {
				cmd.Println("Scanning on-disk audio files and synchronizing tags with database...")
			}
			report, err := sync.SyncLibrary(cmd.Context(), database, libDirFlag)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: complete")
				cmd.Printf("total_tracks: %d\n", report.TotalDBTracks)
				cmd.Printf("updated_records: %d\n", report.UpdatedCount)
				cmd.Printf("missing_files: %d\n", report.MissingCount)
				cmd.Printf("unindexed_files: %d\n", report.UnindexedCount)
				for _, p := range report.UnindexedPaths {
					cmd.Printf("unindexed: %s\n", p)
				}
				return nil
			}

			cmd.Println("Library Sync Complete!")
			cmd.Printf("  Total Tracks in DB:   %d\n", report.TotalDBTracks)
			cmd.Printf("  Updated Records:      %d\n", report.UpdatedCount)
			cmd.Printf("  Missing Files:        %d\n", report.MissingCount)
			cmd.Printf("  Unindexed Disk Files: %d\n", report.UnindexedCount)

			if len(report.UnindexedPaths) > 0 {
				cmd.Println("\nUnindexed audio files found on disk:")
				for idx, p := range report.UnindexedPaths {
					if idx < 15 {
						cmd.Printf("  - %s\n", p)
					}
				}
				if len(report.UnindexedPaths) > 15 {
					cmd.Printf("  ... and %d more files\n", len(report.UnindexedPaths)-15)
				}
			}

			return nil
		},
	}
}
