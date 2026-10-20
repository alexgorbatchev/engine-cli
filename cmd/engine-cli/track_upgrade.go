package main

import (
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/spf13/cobra"
)

func newTrackUpgradeCommand() *cobra.Command {
	flags := append([]acquisitionFlag{{"query", "string", "Search words instead of the saved artist and title"}, {"keep-timing", "bool", "Keep saved cues, loops, and beat grids"}, {"clear-timing", "bool", "Remove saved cues, loops, and beat grids"}}, trackFetchFlags...)
	flags = append(flags, continuationFlags...)
	command := acquisitionCommand("track.upgrade", "upgrade <id|path>", "Fetch replacement audio while keeping track information and playlists", "identifier", flags)
	command.Args = cobra.ExactArgs(1)
	command.MarkFlagsMutuallyExclusive("keep-timing", "clear-timing")
	return command
}

func printTrackUpgrade(cmd *cobra.Command, result map[string]any) error {
	t := result["track"].(map[string]any)
	path := displayPath(t["path"].(string))
	original := displayPath(result["original_path"].(string))
	if agent.IsAgentMode() {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "status: ok\ntrack_id: %d\npath: %s\noriginal_retained: %s\ntiming_cleared: %t\nreanalysis_required: true\n", t["id"], path, original, result["timing_cleared"])
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "[OK] Upgraded track %q [id:%d]\nReplacement: %s\nOriginal audio kept at: %s\nAnalyze the replacement in Engine DJ before playing it.\n", t["title"], t["id"], path, original)
	return err
}
