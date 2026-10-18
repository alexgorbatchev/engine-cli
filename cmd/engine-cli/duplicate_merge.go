package main

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/duplicate"
	"github.com/spf13/cobra"
)

func newDuplicateMergeCommand() *cobra.Command {
	var keep, performance int64
	c := &cobra.Command{Use: "merge <id...>", Short: "Merge selected duplicates into a keeper, retaining audio files", Args: cobra.MinimumNArgs(1),
		Long: "Keep --keep and merge the listed duplicate IDs into it. Transfers playlists and playback history. Audio files stay in place. Keeper cues and grids are preserved unless --performance-from explicitly selects another track's complete performance data. Check timing before playing. Close Engine DJ first. Every merge is recorded for undo.",
		RunE: func(c *cobra.Command, args []string) error {
			ids := make([]int64, len(args))
			for i, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil || id <= 0 {
					return fmt.Errorf("%q is not a positive track ID", arg)
				}
				ids[i] = id
			}
			if _, err := backup.AutoBackupIfNeeded(c.Context(), libDirFlag, noBackupFlag); err != nil {
				return err
			}
			d, err := db.Open(filepath.Join(libDirFlag, "Database2", "m.db"))
			if err != nil {
				return err
			}
			defer d.Close()
			result, err := duplicate.Merge(c.Context(), d, duplicate.MergeOptions{Keep: keep, Remove: ids, PerformanceFrom: performance})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "kept_id: %d\nmerged: %d\naudio_files: retained\n", result.Kept.ID, len(result.Removed))
			return err
		}}
	c.Flags().Int64Var(&keep, "keep", 0, "Track ID to retain")
	c.Flags().Int64Var(&performance, "performance-from", 0, "Explicitly copy cues, loops, and grids from this selected track ID")
	if err := c.MarkFlagRequired("keep"); err != nil {
		panic(err)
	} // The flag is declared immediately above.
	return c
}
