package main

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/spf13/cobra"
)

func printPlaylistExport(command *cobra.Command, result any, asJSON bool) (bool, error) {
	if asJSON {
		return false, nil
	}
	switch value := result.(type) {
	case *playlist.ExportPlan:
		if value != nil {
			return true, printPlaylistExportPlan(command, value)
		}
	case *playlist.ExportResult:
		if value != nil {
			_, err := fmt.Fprintf(command.OutOrStdout(), "Exported %d memberships to %s (%s, %s paths); %d unavailable memberships skipped. Source collection unchanged.\n", value.ExportedCount, displayPath(value.Path), value.Encoding, value.PathMode, value.SkippedCount)
			return true, err
		}
	}
	return false, nil
}

func printPlaylistExportPlan(command *cobra.Command, plan *playlist.ExportPlan) error {
	output := command.OutOrStdout()
	if _, err := fmt.Fprintf(output, "Playlist: %s [id:%d]\nDestination: %s\nEncoding: %s\nMedia paths: %s\nExported memberships: %d\nUnavailable memberships: %d\n", plan.PlaylistTitle, plan.PlaylistID, displayPath(plan.Path), plan.Encoding, plan.PathMode, plan.ExportedCount, plan.UnavailableCount); err != nil {
		return err
	}
	for index, entry := range plan.Entries {
		if _, err := fmt.Fprintf(output, "  %d: %s — %s — %s\n", index+1, entry.Status, entry.Title, displayPath(entry.Path)); err != nil {
			return err
		}
	}
	if !plan.CanApply {
		_, err := fmt.Fprintln(output, "Not applicable: resolve unavailable media or explicitly review with --skip-unavailable.")
		return err
	}
	_, err := fmt.Fprintf(output, "Plan token: %s\nExport using the same identifier, destination, encoding, path mode and skip policy with --plan-token.\nCreates a new file only; existing destinations are never replaced. A new file may be visible while writing.\n", plan.PlanToken)
	return err
}
