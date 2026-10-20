package main

import (
	"encoding/json"
	"errors"
	"fmt"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree"
	"github.com/alexgorbatchev/engine-cli/internal/migrate"
	"github.com/spf13/cobra"
)

func init() {
	techCatalog["engine-cli migrate"] = cobrahelptree.TechInfo{Summary: "Export libraries to other DJ applications"}
	techCatalog["engine-cli migrate export"] = cobrahelptree.TechInfo{Summary: "Copy Engine DJ audio and export Rekordbox XML without changing the source", Description: "Runs unattended; writes Contents/, rekordbox.xml, and .sync_state.json under the target. --source defaults to --lib-dir; --target defaults to ./rekordbox. --json prints the HTTP result format."}
}

func newMigrateCommand() *cobra.Command {
	group := &cobra.Command{Use: "migrate", Short: "Export libraries to other DJ applications", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	var opts migrate.Options
	var asJSON bool
	c := &cobra.Command{Use: "export", Short: "Copy Engine DJ audio and export Rekordbox XML", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if opts.Source == "" {
				opts.Source = libDirFlag
			}
			report, runErr := migrate.Export(c.Context(), opts)
			if runErr != nil {
				runErr = migrationError{runErr}
			}
			if report == nil {
				return runErr
			}
			var outputErr error
			if asJSON {
				outputErr = json.NewEncoder(c.OutOrStdout()).Encode(report)
			} else {
				_, outputErr = fmt.Fprintf(c.OutOrStdout(), "copied: %d\nexisting: %d\nfailed: %d\nunavailable: %d\nxml: %s\n", report.Copied, report.Existing, report.Failed, report.Unavailable, displayPath(report.XML))
			}
			return errors.Join(runErr, outputErr)
		}}
	c.Flags().StringVarP(&opts.Source, "source", "s", "", "Source Engine Library directory (empty: --lib-dir)")
	c.Flags().StringVarP(&opts.Target, "target", "t", "", "Rekordbox output directory (empty: ./rekordbox)")
	c.Flags().BoolVar(&asJSON, "json", false, "Print the migration result as JSON")
	group.AddCommand(c)
	return group
}

type migrationError struct{ error }

func (e migrationError) Error() string { return displayPath(e.error.Error()) }
func (e migrationError) Unwrap() error { return e.error }
