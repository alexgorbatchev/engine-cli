package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/verify"
	"github.com/spf13/cobra"
)

func newVerifyCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Run full integrity audit on Engine DJ database and audio files",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath, true)
			if err != nil {
				return err
			}
			defer database.Close()

			report, err := verify.VerifyLibrary(cmd.Context(), database, libDirFlag)
			if err != nil {
				return err
			}

			if jsonOutput {
				out, errJSON := json.MarshalIndent(report, "", "  ")
				if errJSON != nil {
					return errJSON
				}
				fmt.Println(string(out))
				return nil
			}

			fmt.Println("=== Engine DJ Library Integrity Report ===")
			fmt.Printf("  Total Tracks in DB:   %d\n", report.TotalTracks)
			fmt.Printf("  Total Playlists:      %d\n", report.TotalPlaylists)
			fmt.Printf("  Missing Files on Disk:%d\n", report.MissingOnDiskCount)
			fmt.Printf("  File Size Mismatches: %d\n", report.BytesMismatchCount)
			fmt.Printf("  Missing Album Art:    %d\n", report.MissingArtCount)
			fmt.Printf("  Orphaned Entities:    %d\n", report.OrphanedCount)
			fmt.Printf("  Empty Playlists:      %d\n", report.EmptyPlaylistCount)

			if report.IsHealthy {
				fmt.Println("\nSTATUS: Library is 100% HEALTHY.")
			} else {
				fmt.Printf("\nSTATUS: Found %d issues.\n\n", len(report.Issues))
				fmt.Println("Issues Summary:")
				for idx, issue := range report.Issues {
					if idx < 25 {
						fmt.Printf("  - [%s] %s\n", issue.Type, issue.Description)
					}
				}
				if len(report.Issues) > 25 {
					fmt.Printf("  ... and %d more issues (use --json for full list)\n", len(report.Issues)-25)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output report in JSON format")

	return cmd
}
