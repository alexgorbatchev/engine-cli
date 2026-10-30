package main

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func newFindReplaceCommand() *cobra.Command {
	var fields []string
	var find, replacement, mode, token string
	var matchCase, dryRun, asJSON bool
	c := &cobra.Command{Use: "find-replace <tr|id|path...>", Short: "Review and apply per-track text replacements as one undoable edit", Args: cobra.RangeArgs(1, track.MaxMetadataBatch), Annotations: map[string]string{"engine-api-operation": "true"}}
	c.Flags().StringSliceVar(&fields, "fields", nil, "Text fields: title, artist, album, genre, comment, label, composer, remixer")
	c.Flags().StringVar(&find, "find", "", "Nonempty text or Go regular expression to find")
	c.Flags().StringVar(&replacement, "replacement", "", "Replacement text; empty removes matches; regex supports $1 and ${name}")
	c.Flags().StringVar(&mode, "mode", "literal", "Match mode: literal or regex")
	c.Flags().BoolVar(&matchCase, "match-case", false, "Match case exactly (default: Unicode case-insensitive)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Review replacements without changing the collection")
	c.Flags().StringVar(&token, "plan-token", "", "Apply the exact reviewed plan; required without --dry-run")
	c.Flags().BoolVar(&asJSON, "json", false, "Print the review or affected track IDs as JSON")
	c.RunE = func(c *cobra.Command, args []string) error {
		if !c.Flags().Changed("replacement") {
			return fmt.Errorf("supply --replacement, including an empty string to remove matches")
		}
		return runOperation(c, "track.batch.find-replace", map[string]any{"identifiers": args, "fields": fields, "find": find, "replacement": replacement, "mode": mode, "match_case": matchCase, "dry_run": dryRun, "plan_token": token}, asJSON, "")
	}
	return c
}

func printFindReplace(c *cobra.Command, result any, asJSON bool) (bool, error) {
	if asJSON {
		return false, nil
	}
	switch r := result.(type) {
	case *track.FindReplacePlan:
		if _, err := fmt.Fprintf(c.OutOrStdout(), "Selected tracks: %d\nChanged tracks: %d\nChanged fields: %d\n", r.SelectedTracks, r.ChangedTracks, r.ChangedFields); err != nil {
			return true, err
		}
		for _, tr := range r.Tracks {
			for _, change := range tr.Changes {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "Track %d, %s: %q -> %q\n", tr.TrackID, change.Field, change.Before, change.After); err != nil {
					return true, err
				}
				if change.ErrorCode != "" {
					if _, err := fmt.Fprintln(c.OutOrStdout(), "  Blocked: track titles must contain text."); err != nil {
						return true, err
					}
				}
			}
		}
		if !r.CanApply {
			_, err := fmt.Fprintln(c.OutOrStdout(), "Cannot apply: review matching changes and resolve invalid titles.")
			return true, err
		}
		_, err := fmt.Fprintf(c.OutOrStdout(), "Plan token: %s\nApply identical options with --plan-token and without --dry-run.\n", r.PlanToken)
		return true, err
	case *track.FindReplaceResult:
		_, err := fmt.Fprintf(c.OutOrStdout(), "Updated %d fields across %d tracks. One undo restores the complete edit.\n", r.ChangedFields, len(r.TrackIDs))
		return true, err
	}
	return false, nil
}
