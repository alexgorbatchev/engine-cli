package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

// runBatchMetadataEdit runs the shared track.batch.update operation with the command's selection and fields.
func runBatchMetadataEdit(c *cobra.Command, edit track.BatchMetadataEdit, asJSON bool) error {
	encoded, err := json.Marshal(edit)
	if err != nil {
		return err
	}
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		return err
	}
	for name, value := range values {
		if value == nil {
			delete(values, name)
		}
	}
	return runOperation(c, "track.batch.update", values, asJSON, "")
}

// printBatchEdit prints a batch edit's review or result; JSON output keeps the operation's own shape.
func printBatchEdit(c *cobra.Command, result any, asJSON bool) (bool, error) {
	if asJSON {
		return false, nil
	}
	out := c.OutOrStdout()
	switch r := result.(type) {
	case *track.BatchEditPlan:
		return true, printBatchEditPlan(out, r)
	case *track.BatchEditResult:
		if agent.IsAgentMode() {
			_, err := fmt.Fprintf(out, "status: ok\ntrack_ids: %v\nchanged_fields: %d\n", r.TrackIDs, r.ChangedFields)
			return true, err
		}
		if len(r.TrackIDs) == 0 {
			_, err := fmt.Fprintln(out, "Nothing changed: the selected tracks already have these values.")
			return true, err
		}
		_, err := fmt.Fprintf(out, "Updated %d fields across %d tracks. Undo reverses the complete edit.\n", r.ChangedFields, len(r.TrackIDs))
		return true, err
	}
	return false, nil
}

func printBatchEditPlan(out io.Writer, plan *track.BatchEditPlan) error {
	if _, err := fmt.Fprintf(out, "Selected tracks: %d\nChanged tracks: %d\nChanged fields: %d\n", plan.SelectedTracks, plan.ChangedTracks, plan.ChangedFields); err != nil {
		return err
	}
	for _, tr := range plan.Tracks {
		for _, change := range tr.Changes {
			if _, err := fmt.Fprintf(out, "Track %d, %s: %s -> %s\n", tr.TrackID, change.Field, reviewedValue(change.Before), reviewedValue(change.After)); err != nil {
				return err
			}
		}
	}
	for _, missing := range plan.Unresolved {
		if _, err := fmt.Fprintf(out, "Not in this library: %s\n", missing.Identifier); err != nil {
			return err
		}
	}
	if !plan.CanApply {
		_, err := fmt.Fprintln(out, "Cannot apply: choose tracks in this library and values that change them.")
		return err
	}
	_, err := fmt.Fprintf(out, "Plan token: %s\nApply identical options with --plan-token and without --dry-run.\n", plan.PlanToken)
	return err
}

// reviewedValue shows an empty field as "(empty)" and text quoted.
func reviewedValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "(empty)"
	case string:
		return fmt.Sprintf("%q", v)
	}
	return fmt.Sprint(value)
}
