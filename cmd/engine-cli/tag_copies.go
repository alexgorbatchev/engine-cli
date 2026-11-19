package main

import (
	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v4"
	"github.com/spf13/cobra"
)

func newTagCopiesCommand() *cobra.Command {
	group := &cobra.Command{Use: "tag-copies", Short: "Fold the copies earlier tag edits left beside tracks", Args: cobra.NoArgs}
	group.AddCommand(acquisitionCommand("library.tag-copies.repair", "repair", "Fold '-upgraded-' tag-edit copies back into their original files", "", []acquisitionFlag{
		{"dry-run", "bool", "Review every rename and removal without changing anything"},
		{"plan-token", "string", "Exact token returned by --dry-run"},
	}))
	techCatalog["engine-cli library tag-copies repair"] = cobrahelptree.TechInfo{Summary: "Review or apply folding tag-edit copies whose audio matches the original, bound to a plan token", MutatesDB: true}
	return group
}
