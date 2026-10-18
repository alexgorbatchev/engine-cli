package main

import (
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func newTrackUpgradeCommand() *cobra.Command {
	var query string
	var keepTiming, clearTiming bool
	cmd := &cobra.Command{
		Use:   "upgrade <id|path>",
		Short: "Fetch replacement audio while keeping your track information and playlists",
		Long: `Find and download replacement audio using Fetch Track CLI.

Your original track information, artwork, and playlist membership are preserved.
An extended mix may replace a radio edit. The original audio file is kept.
Close Engine DJ before running this command.
Analyze the replacement in Engine DJ before playing it.
If the track has saved cues, loops, or beat grids, choose --keep-timing to
retain them or --clear-timing to remove them. An extended mix may shift their positions.

Requires fetch-track, ffmpeg, and ffprobe on your PATH.
Automatically saves a safety backup before making changes unless --no-backup is specified.`,
		Example: `  engine-cli track upgrade 42
  engine-cli track upgrade 42 --query "Sample Artist Sample Title extended mix"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bk, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before track upgrade: %w", err)
			}
			if bk != nil {
				if agent.IsAgentMode() {
					cmd.Printf("backup_snapshot: %s\n", displayPath(bk.Path))
				} else {
					cmd.Printf("Created automated backup snapshot at %s\n", displayPath(bk.Path))
				}
			}
			database, err := db.Open(filepath.Join(libDirFlag, "Database2", "m.db"))
			if err != nil {
				return err
			}
			defer database.Close()
			result, err := track.UpgradeTrack(cmd.Context(), database, libDirFlag, args[0], track.UpgradeOptions{Query: query, KeepTiming: keepTiming, ClearTiming: clearTiming})
			if err != nil {
				return err
			}
			return printTrackUpgrade(cmd, result)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "Search words to use instead of the original artist and title")
	cmd.Flags().BoolVar(&keepTiming, "keep-timing", false, "Keep saved cues, loops, and beat grids; check their positions before playing")
	cmd.Flags().BoolVar(&clearTiming, "clear-timing", false, "Remove saved cues, loops, and beat grids from the upgraded track")
	cmd.MarkFlagsMutuallyExclusive("keep-timing", "clear-timing")
	return cmd
}

func printTrackUpgrade(cmd *cobra.Command, result *track.UpgradeResult) error {
	path := displayPath(result.Track.PathString())
	original := displayPath(result.OriginalPath)
	if agent.IsAgentMode() {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "status: ok\ntrack_id: %d\npath: %s\noriginal_retained: %s\ntiming_cleared: %t\nreanalysis_required: true\n", result.Track.ID, path, original, result.TimingCleared)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "[OK] Upgraded track %q [id:%d]\nReplacement: %s\nOriginal audio kept at: %s\nAnalyze the replacement in Engine DJ before playing it.\n", result.Track.Title.String, result.Track.ID, path, original)
	return err
}
