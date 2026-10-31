package main

import (
	"fmt"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v3"
	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/spf13/cobra"
)

func newSmartlistRepairCommand() *cobra.Command {
	group := &cobra.Command{Use: "smartlist-repair", Short: "Review, repair, or recover damaged smart playlists", Args: cobra.NoArgs}
	group.AddCommand(
		acquisitionCommand("library.smartlist-repair.preview", "preview", "Review smart playlists that would be reconnected or removed", "", nil),
		acquisitionCommand("library.smartlist-repair.apply", "apply", "Apply the exact reviewed smart-playlist repair", "", []acquisitionFlag{
			{"plan-token", "string", "Exact token returned by smartlist-repair preview"},
		}),
		acquisitionCommand("library.smartlist-repair.recover", "recover", "Roll back an interrupted smart-playlist repair using its retained originals", "", nil),
	)
	techCatalog["engine-cli library smartlist-repair preview"] = cobrahelptree.TechInfo{Summary: "Read-only review of native Smartlist repair and original-delete amendments"}
	techCatalog["engine-cli library smartlist-repair apply"] = cobrahelptree.TechInfo{Summary: "Apply exact reviewed repair with immutable originals and reconnect-only Undo", MutatesDB: true}
	techCatalog["engine-cli library smartlist-repair recover"] = cobrahelptree.TechInfo{Summary: "Validate retained originals and all targets, then roll back interrupted repair", MutatesDB: true}
	return group
}

func printSmartlistRepair(command *cobra.Command, result any) (bool, error) {
	var review *history.SmartlistRepairReview
	var text string
	switch value := result.(type) {
	case *history.SmartlistRepairReview:
		review = value
	case *history.SmartlistRepairResult:
		review = &value.SmartlistRepairReview
		if agent.IsAgentMode() {
			text = fmt.Sprintf("applied: %t\n", value.Applied)
		} else {
			text = fmt.Sprintf("Applied: %t\n", value.Applied)
		}
	case *history.SmartlistRepairRecovery:
		if agent.IsAgentMode() {
			text = fmt.Sprintf("rolled_back: %t\nrestored_targets: %d\n", value.RolledBack, value.RestoredTargets)
		} else {
			text = fmt.Sprintf("Rolled back interrupted repair: %t\nRestored files: %d\n", value.RolledBack, value.RestoredTargets)
		}
	default:
		return false, nil
	}
	if review != nil {
		text += formatSmartlistRepairReview(review)
	}
	_, err := fmt.Fprint(command.OutOrStdout(), text)
	return true, err
}

func formatSmartlistRepairReview(review *history.SmartlistRepairReview) string {
	var text string
	if agent.IsAgentMode() {
		text = fmt.Sprintf("reconnected: %d\nremoved: %d\namended_deletes: %d\nplan_token: %s\n", review.Reconnected, review.Removed, review.AmendedDeletes, review.PlanToken)
	} else {
		text = fmt.Sprintf("Reconnected smart playlists: %d\nRemoved damaged smart playlists: %d\nUpdated deletion operations: %d\nPlan token: %s\n", review.Reconnected, review.Removed, review.AmendedDeletes, review.PlanToken)
	}
	for _, row := range review.Rows {
		if agent.IsAgentMode() {
			text += fmt.Sprintf("%s: %q uuid=%q delete_entry=%s\n", row.Action, row.Name, row.UUID, row.DeleteEntryID)
		} else {
			text += fmt.Sprintf("%s: %q [uuid:%q]\n", row.Action, row.Name, row.UUID)
			if row.DeleteEntryID != "" {
				text += fmt.Sprintf("  Original deletion operation: %s\n", row.DeleteEntryID)
			}
		}
	}
	return text
}
