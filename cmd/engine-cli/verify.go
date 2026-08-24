package main

import (
	"encoding/json"
	"path/filepath"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/verify"
	"github.com/spf13/cobra"
)

func newVerifyCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check your library for missing songs, broken links, and empty playlists",
		Long: `Run a full health check on your Engine DJ music collection.

Checks for:
  - Audio files that were moved or deleted from your computer or USB drive
  - Audio files whose size changed after being edited
  - Missing album cover art
  - Empty playlists that do not have any songs`,
		Example: `  engine-cli verify
  engine-cli verify --json
  engine-cli -l "/Volumes/DJ_USB/Engine Library" verify`,
		Args: cobra.NoArgs,
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
				cmd.Println(string(out))
				return nil
			}

			if agent.IsAgentMode() {
				statusStr := "healthy"
				if !report.IsHealthy {
					statusStr = "issues_found"
				}
				cmd.Printf("status: %s\n", statusStr)
				cmd.Printf("total_tracks: %d\n", report.TotalTracks)
				cmd.Printf("total_playlists: %d\n", report.TotalPlaylists)
				cmd.Printf("missing_files: %d\n", report.MissingOnDiskCount)
				cmd.Printf("size_mismatches: %d\n", report.BytesMismatchCount)
				cmd.Printf("missing_art: %d\n", report.MissingArtCount)
				cmd.Printf("orphaned_entities: %d\n", report.OrphanedCount)
				cmd.Printf("empty_playlists: %d\n", report.EmptyPlaylistCount)
				for _, issue := range report.Issues {
					cmd.Printf("issue: [%s] %s\n", issue.Type, issue.Description)
				}
				return nil
			}

			cmd.Printf("Total Tracks in DB:   %d\n", report.TotalTracks)
			cmd.Printf("Total Playlists:      %d\n", report.TotalPlaylists)
			cmd.Printf("Missing Files on Disk:%d\n", report.MissingOnDiskCount)
			cmd.Printf("File Size Mismatches: %d\n", report.BytesMismatchCount)
			cmd.Printf("Missing Album Art:    %d\n", report.MissingArtCount)
			cmd.Printf("Orphaned Entities:    %d\n", report.OrphanedCount)
			cmd.Printf("Empty Playlists:      %d\n", report.EmptyPlaylistCount)

			if report.IsHealthy {
				cmd.Println("\nSTATUS: Library is 100% HEALTHY.")
			} else {
				cmd.Printf("\nSTATUS: Found %d issues.\n\n", len(report.Issues))
				cmd.Println("Issues Summary:")
				for idx, issue := range report.Issues {
					if idx < 25 {
						cmd.Printf("  - [%s] %s\n", issue.Type, issue.Description)
					}
				}
				if len(report.Issues) > 25 {
					cmd.Printf("  ... and %d more issues (use --json for full list)\n", len(report.Issues)-25)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output report in JSON format")

	return cmd
}
