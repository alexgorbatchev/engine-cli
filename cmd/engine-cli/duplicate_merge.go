package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newDuplicateMergeCommand() *cobra.Command {
	var keep, performance int64
	var keepRef, performanceRef string
	c := &cobra.Command{Use: "merge <id...>", Short: "Merge selected duplicates into a keeper, retaining audio files", Args: cobra.MinimumNArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
		Long:        "Keep --keep and merge the listed duplicate IDs into it. Transfers playlists and playback history. Audio files stay in place. Keeper cues and grids are preserved unless --performance-from explicitly selects another track's complete performance data. Check timing before playing. Close Engine DJ first. Every merge is recorded for undo.",
		RunE: func(c *cobra.Command, args []string) error {
			ids := make([]int64, len(args))
			var refs []string
			for i, arg := range args {
				if strings.HasPrefix(arg, "eng:") {
					refs = append(refs, arg)
					continue
				}
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil || id <= 0 {
					return fmt.Errorf("%q is not a positive track ID", arg)
				}
				ids[i] = id
			}
			if len(refs) > 0 {
				if len(refs) != len(args) {
					return fmt.Errorf("choose references or numeric IDs for removed tracks")
				}
				ids = nil
			}
			return runOperation(c, "track.duplicate.merge", map[string]any{"keep": keep, "remove": ids, "performance_from": performance, "keep_ref": keepRef, "remove_refs": refs, "performance_from_ref": performanceRef}, false, "")
		}}
	c.Flags().Int64Var(&keep, "keep", 0, "Track ID to retain")
	c.Flags().Int64Var(&performance, "performance-from", 0, "Explicitly copy cues, loops, and grids from this selected track ID")
	c.Flags().StringVar(&keepRef, "keep-ref", "", "Stable reference of the track to retain")
	c.Flags().StringVar(&performanceRef, "performance-from-ref", "", "Stable reference to explicitly copy performance data from")
	c.MarkFlagsMutuallyExclusive("keep", "keep-ref")
	c.MarkFlagsOneRequired("keep", "keep-ref")
	c.MarkFlagsMutuallyExclusive("performance-from", "performance-from-ref")
	return c
}
