package main

import (
	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func newCapitalizationCommand() *cobra.Command {
	var fields []string
	var recipe, language, token string
	var dryRun, asJSON bool
	c := &cobra.Command{Use: "capitalize <tr|id|path...>", Short: "Review and apply per-track capitalization as one undoable edit", Args: cobra.RangeArgs(1, track.MaxMetadataBatch), Annotations: map[string]string{"engine-api-operation": "true"}}
	c.Flags().StringSliceVar(&fields, "fields", nil, "Text fields: title, artist, album, genre, comment, label, composer, remixer")
	c.Flags().StringVar(&recipe, "recipe", "", "Capitalization: upper, lower or title (required)")
	c.Flags().StringVar(&language, "language", "", "Language tag such as tr or nl; empty uses neutral Unicode rules")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Review capitalization without changing the collection")
	c.Flags().StringVar(&token, "plan-token", "", "Apply the exact reviewed plan; required without --dry-run")
	c.Flags().BoolVar(&asJSON, "json", false, "Print the review or affected track IDs as JSON")
	c.RunE = func(c *cobra.Command, args []string) error {
		return runOperation(c, "track.batch.capitalize", map[string]any{"identifiers": args, "fields": fields, "recipe": recipe, "language": language, "dry_run": dryRun, "plan_token": token}, asJSON, "")
	}
	return c
}
