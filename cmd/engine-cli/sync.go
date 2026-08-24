package main

import (
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/sync"
	"github.com/spf13/cobra"
)

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Synchronize on-disk audio tags and file sizes into SQLite database",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			fmt.Println("Scanning on-disk audio files and synchronizing tags with database...")
			report, err := sync.SyncLibrary(cmd.Context(), database, libDirFlag)
			if err != nil {
				return err
			}

			fmt.Println("Library Sync Complete!")
			fmt.Printf("  Total Tracks in DB:   %d\n", report.TotalDBTracks)
			fmt.Printf("  Updated Records:      %d\n", report.UpdatedCount)
			fmt.Printf("  Missing Files:        %d\n", report.MissingCount)
			fmt.Printf("  Unindexed Disk Files: %d\n", report.UnindexedCount)

			if len(report.UnindexedPaths) > 0 {
				fmt.Println("\nUnindexed audio files found on disk:")
				for idx, p := range report.UnindexedPaths {
					if idx < 15 {
						fmt.Printf("  - %s\n", p)
					}
				}
				if len(report.UnindexedPaths) > 15 {
					fmt.Printf("  ... and %d more files\n", len(report.UnindexedPaths)-15)
				}
			}

			return nil
		},
	}
}
