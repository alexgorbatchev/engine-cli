package main

import (
	"encoding/json"
	"fmt"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v3"
	"github.com/alexgorbatchev/engine-cli/internal/fixturecapture"
	"github.com/spf13/cobra"
)

func newLibraryFixtureCommand() *cobra.Command {
	group := &cobra.Command{Use: "fixture", Short: "Capture offline Desktop evidence for private review", Args: cobra.NoArgs}
	var before, after, output string
	var jsonOutput bool
	command := &cobra.Command{Use: "capture", Short: "Archive and inspect an offline before/after handover", Long: `Capture explicitly offline Desktop handover folders into a new private evidence directory.

Quit Desktop and finish copying the handover before running this command. Inputs
are archived without opening their databases. Journal recovery and inspection run
only on separate working copies. Observed input changes or unsafe entries refuse
the capture; this is not an atomic backup of a live library. Failed output is
retained and cannot be reused. Raw evidence may contain personal information and
must be reviewed before publication. No library registration, backup, or Undo
record is created.`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		report, err := fixturecapture.Capture(cmd.Context(), before, after, output)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Captured private offline evidence in %q\nBefore files: %d\nAfter files: %d\nReview privacy before publishing any extracted fixtures.\n", displayPath(output), len(report.Before.Files), len(report.After.Files))
		return err
	}}
	command.Flags().StringVar(&before, "before", "", "Offline handover folder before Desktop edits")
	command.Flags().StringVar(&after, "after", "", "Offline handover folder after Desktop edits")
	command.Flags().StringVar(&output, "out-dir", "", "New private evidence directory (must not exist)")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Print the capture inventory as JSON")
	for _, flag := range []string{"before", "after", "out-dir"} {
		if err := command.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	group.AddCommand(command)
	techCatalog["engine-cli library fixture capture"] = cobrahelptree.TechInfo{Summary: "Archive offline inputs without SQLite access; recover copies and export exact typed table values; private output only"}
	return group
}
