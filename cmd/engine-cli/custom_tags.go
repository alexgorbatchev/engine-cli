package main

import (
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/tags"
	"github.com/spf13/cobra"
)

func newCustomTagsCommand() *cobra.Command {
	root := &cobra.Command{Use: "tag", Short: "Manage library-local custom categories and named values", Args: cobra.NoArgs}
	category := &cobra.Command{Use: "category", Short: "Manage custom tag categories", Args: cobra.NoArgs}
	category.AddCommand(acquisitionCommand("tag.category.list", "list", "List categories, values, assignment counts and current revision", "", nil))
	value := &cobra.Command{Use: "value", Short: "Manage named values within custom tag categories", Args: cobra.NoArgs}
	for _, group := range []struct {
		command *cobra.Command
		subject string
	}{{category, "category"}, {value, "value"}} {
		revision := acquisitionFlag{"revision", "string", "Current custom-tag revision from category list; rejects concurrent changes"}
		createFlags := []acquisitionFlag{revision}
		if group.subject == "value" {
			createFlags = append(createFlags, acquisitionFlag{"category", "string", "Destination category ID"})
		}
		group.command.AddCommand(acquisitionCommand("tag."+group.subject+".create", "create <name>", "Create a named custom tag "+group.subject, "name", createFlags))
		group.command.AddCommand(acquisitionCommand("tag."+group.subject+".update", "update <id>", "Rename a custom tag "+group.subject, "id", []acquisitionFlag{revision, {"name", "string", "New name"}}))
		group.command.AddCommand(acquisitionCommand("tag."+group.subject+".delete", "delete <id>", "Review and delete a custom tag "+group.subject+" and its assignments", "id", []acquisitionFlag{revision, {"dry-run", "bool", "Review affected tracks and assignments without changing the library"}, {"plan-token", "string", "Exact deletion review token; required without --dry-run"}}))
	}
	root.AddCommand(category, value)
	root.AddCommand(acquisitionCommand("tag.reconcile", "reconcile", "Review and remove assignments for tracks removed outside Engine CLI", "", []acquisitionFlag{{"revision", "string", "Current custom-tag revision"}, {"dry-run", "bool", "Review unavailable tracks without changing assignments"}, {"plan-token", "string", "Exact reconciliation review token; required without --dry-run"}}))
	return root
}

func newTrackCustomTagsCommand() *cobra.Command {
	root := &cobra.Command{Use: "custom-tags", Short: "Inspect and change library-local custom values for selected tracks", Args: cobra.NoArgs}
	root.AddCommand(acquisitionCommand("track.custom-tags.inspect", "inspect <track>", "Inspect custom values and current taxonomy revision", "track", nil))
	root.AddCommand(acquisitionCommand("track.custom-tags.update", "update <track>", "Replace all custom values on one track; --values= clears them", "track", []acquisitionFlag{{"values", "strings", "Complete value ID list; repeat or use commas, explicit empty clears all"}, {"revision", "string", "Current custom-tag revision from inspection or category list"}}))
	root.AddCommand(acquisitionCommand("track.custom-tags.batch-inspect", "batch-inspect", "Inspect mixed custom-tag assignments on selected tracks", "", []acquisitionFlag{{"tracks", "strings", "Selected track IDs or selectors, in selection order (1–500)"}}))
	root.AddCommand(acquisitionCommand("track.custom-tags.batch-update", "batch-update", "Review and atomically add or remove selected custom-tag values", "", []acquisitionFlag{{"tracks", "strings", "Selected track IDs or selectors, in selection order (1–500)"}, {"revision", "string", "Current custom-tag revision"}, {"add", "strings", "Value IDs to add; explicit empty preserves all other assignments"}, {"remove", "strings", "Value IDs to remove; explicit empty preserves all other assignments"}, {"dry-run", "bool", "Review exact assignment impact without changing the library"}, {"plan-token", "string", "Exact batch review token; required without --dry-run"}}))
	return root
}

func printCustomTags(command *cobra.Command, result any) (bool, error) {
	output := command.OutOrStdout()
	switch value := result.(type) {
	case *tags.Taxonomy:
		if _, err := fmt.Fprintf(output, "Custom tags: %d categories\nUnavailable tracks: %d\nRevision: %s\n", len(value.Categories), value.UnavailableTracks, value.Revision); err != nil {
			return true, err
		}
		for _, category := range value.Categories {
			if _, err := fmt.Fprintf(output, "%s [%s]\n", category.Name, category.ID); err != nil {
				return true, err
			}
			for _, v := range category.Values {
				if _, err := fmt.Fprintf(output, "  %s [%s] — %d tracks\n", v.Name, v.ID, v.TrackCount); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	case *tags.TrackTags:
		_, err := fmt.Fprintf(output, "Track: %d\nCustom values: %s\nRevision: %s\n", value.TrackID, strings.Join(value.Values, ", "), value.Revision)
		return true, err
	case *tags.BatchTags:
		if _, err := fmt.Fprintf(output, "Selected tracks: %v\nRevision: %s\n", value.TrackIDs, value.Revision); err != nil {
			return true, err
		}
		for _, category := range value.Categories {
			if _, err := fmt.Fprintf(output, "%s [%s]\n", category.Name, category.ID); err != nil {
				return true, err
			}
			for _, v := range category.Values {
				if _, err := fmt.Fprintf(output, "  %s [%s] — %d of %d selected tracks\n", v.Name, v.ID, v.SelectedTracks, len(value.TrackIDs)); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	case *tags.BatchChange:
		_, err := fmt.Fprintf(output, "Selected tracks: %v\nAdd values: %s\nRemove values: %s\nChanged tracks: %d\nAdded assignments: %d\nRemoved assignments: %d\nApplied: %t\nRevision: %s\nPlan token: %s\n", value.TrackIDs, strings.Join(value.Add, ", "), strings.Join(value.Remove, ", "), value.ChangedTracks, value.AddedAssignments, value.RemovedAssignments, value.Applied, value.Revision, value.PlanToken)
		return true, err
	case *tags.Mutation:
		_, err := fmt.Fprintf(output, "Custom tag: %s\nChanged: %t\nRevision: %s\n", value.ID, value.Changed, value.Revision)
		return true, err
	case *tags.Deletion:
		_, err := fmt.Fprintf(output, "Custom tag: %s\nAffected tracks: %d\nRemoved assignments: %d\nRemoved values: %d\nApplied: %t\nRevision: %s\nPlan token: %s\n", value.ID, value.AffectedTracks, value.RemovedAssignments, value.RemovedValues, value.Applied, value.Revision, value.PlanToken)
		return true, err
	case *tags.Reconciliation:
		_, err := fmt.Fprintf(output, "Unavailable tracks: %d\nRemoved assignments: %d\nApplied: %t\nRevision: %s\nPlan token: %s\n", value.UnavailableTracks, value.RemovedAssignments, value.Applied, value.Revision, value.PlanToken)
		return true, err
	default:
		return false, nil
	}
}
