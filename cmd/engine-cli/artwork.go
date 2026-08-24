package main

import (
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/artwork"
	"github.com/dj/engine-cli/internal/db"
	"github.com/spf13/cobra"
)

func newArtworkCommand() *cobra.Command {
	artCmd := &cobra.Command{
		Use:   "artwork",
		Short: "Manage and fix Engine DJ album artwork and cache",
	}

	artCmd.AddCommand(newArtworkNormalizeCommand())
	artCmd.AddCommand(newArtworkPruneCommand())

	return artCmd
}

func newArtworkNormalizeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "normalize",
		Short: "Extract embedded artwork, crop to 1:1 square (1400x1400), and update AlbumArt table and disk cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			artCacheDir := filepath.Join(libDirFlag, "Artwork")

			fmt.Println("Normalizing collection artwork to 1:1 square (1400x1400)...")
			report, err := artwork.NormalizeArtwork(cmd.Context(), database, libDirFlag, artCacheDir)
			if err != nil {
				return err
			}

			fmt.Println("Artwork Normalization Complete!")
			fmt.Printf("  Total Tracks:     %d\n", report.TotalTracks)
			fmt.Printf("  Normalized to 1:1:%d\n", report.NormalizedCount)
			fmt.Printf("  Already Square:   %d\n", report.AlreadySquare)
			fmt.Printf("  No Art / Audio:   %d\n", report.NoArtCount)
			if report.ErrorCount > 0 {
				fmt.Printf("  Errors / Skipped: %d\n", report.ErrorCount)
			}

			return nil
		},
	}
}

func newArtworkPruneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Delete orphaned AlbumArt database rows and stale cached thumbnail files",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			artCacheDir := filepath.Join(libDirFlag, "Artwork")

			fmt.Println("Pruning orphaned album artwork records and stale cache files...")
			report, err := artwork.PruneArtwork(cmd.Context(), database, artCacheDir)
			if err != nil {
				return err
			}

			fmt.Println("Artwork Prune Complete!")
			fmt.Printf("  Deleted DB Rows:    %d\n", report.DeletedDBRows)
			fmt.Printf("  Deleted Disk Files: %d\n", report.DeletedDiskFiles)

			return nil
		},
	}
}
