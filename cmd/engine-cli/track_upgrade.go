package main

import (
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/api"
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

func printTrackUpgrade(cmd *cobra.Command, result api.UpgradedTrack) error {
	path := displayPath(result.Path)
	original := displayPath(result.OriginalPath)
	if agent.IsAgentMode() {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "status: ok\ntrack_id: %d\npath: %s\noriginal_retained: %s\ntiming_cleared: %t\nreanalysis_required: %t\n", result.TrackID, path, original, result.TimingCleared, result.ReanalysisRequired)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "[OK] Upgraded track [id:%d]\nReplacement: %s\nOriginal audio kept at: %s\nAnalyze the replacement in Engine DJ before playing it.\n", result.TrackID, path, original)
	return err
}
