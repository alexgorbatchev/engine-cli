package main

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/spf13/cobra"
)

func newTrackColorCommand() *cobra.Command {
	root := &cobra.Command{Use: "color", Short: "Manage library-local track colors", Args: cobra.NoArgs}
	root.AddCommand(acquisitionCommand("track.color.list", "list", "List assigned track colors and current revision", "", nil))
	root.AddCommand(acquisitionCommand("track.color.inspect", "inspect <track>", "Inspect one track's color and current revision", "track", nil))
	root.AddCommand(acquisitionCommand("track.color.update", "update <track>", "Set a track color; --color= clears it", "track", []acquisitionFlag{
		{"color", "string", "Color as #RRGGBB; explicit empty clears the current color"},
		{"revision", "string", "Current color revision from list or inspection"},
	}))
	root.AddCommand(acquisitionCommand("track.color.reconcile", "reconcile", "Review and remove colors for tracks removed outside Engine CLI", "", []acquisitionFlag{
		{"revision", "string", "Current color revision"},
		{"dry-run", "bool", "Review unavailable-track colors without changing the library"},
		{"plan-token", "string", "Exact reconciliation review token; required without --dry-run"},
	}))
	return root
}

func printTrackColors(command *cobra.Command, result any) (bool, error) {
	output := command.OutOrStdout()
	switch value := result.(type) {
	case *colors.Collection:
		if _, err := fmt.Fprintf(output, "Assigned tracks: %d\nUnavailable tracks: %d\nRevision: %s\n", len(value.Tracks), value.UnavailableTracks, value.Revision); err != nil {
			return true, err
		}
		for _, track := range value.Tracks {
			if _, err := fmt.Fprintf(output, "Track %d: %s\n", track.TrackID, track.Color); err != nil {
				return true, err
			}
		}
		return true, nil
	case *colors.TrackColor:
		color := value.Color
		if color == "" {
			color = "unassigned"
		}
		_, err := fmt.Fprintf(output, "Track: %d\nColor: %s\nChanged: %t\nRevision: %s\n", value.TrackID, color, value.Changed, value.Revision)
		return true, err
	case *colors.Reconciliation:
		_, err := fmt.Fprintf(output, "Unavailable tracks: %d\nRemoved colors: %d\nApplied: %t\nRevision: %s\nPlan token: %s\n", value.UnavailableTracks, value.RemovedAssignments, value.Applied, value.Revision, value.PlanToken)
		return true, err
	default:
		return false, nil
	}
}
