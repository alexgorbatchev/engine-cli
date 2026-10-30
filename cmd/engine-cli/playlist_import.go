package main

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/spf13/cobra"
)

func printPlaylistImportPlan(command *cobra.Command, plan *playlist.ImportPlan) error {
	output := command.OutOrStdout()
	if _, err := fmt.Fprintf(output, "New playlist: %s\nSource: %s\nEncoding: %s\nMatched tracks: %d\nRepeated entries omitted: %d\nUnresolved entries: %d\n", plan.Name, plan.Path, plan.Encoding, len(plan.TrackIDs), plan.DuplicateCount, plan.UnresolvedCount); err != nil {
		return err
	}
	for _, entry := range plan.Entries {
		if _, err := fmt.Fprintf(output, "  line %d: %s — %s\n", entry.Line, entry.Status, entry.Path); err != nil {
			return err
		}
	}
	if !plan.CanApply {
		_, err := fmt.Fprintln(output, "Not applicable: resolve unmatched entries or explicitly review with --skip-missing; at least one matched track is required.")
		return err
	}
	_, err := fmt.Fprintf(output, "Plan token: %s\nApply using the same path, name, parent, encoding and skip policy with --plan-token.\n", plan.PlanToken)
	return err
}
