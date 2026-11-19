package main

import (
	"encoding/json"
	"errors"
	"fmt"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v4"
	"github.com/alexgorbatchev/engine-cli/internal/migrate"
	"github.com/spf13/cobra"
)

func init() {
	techCatalog["engine-cli migrate"] = cobrahelptree.TechInfo{Summary: "Import and export DJ application libraries"}
	techCatalog["engine-cli migrate import"] = cobrahelptree.TechInfo{Summary: "Review and import Rekordbox XML into a new playlist hierarchy", Args: []cobrahelptree.ArgSpec{{Name: "<path>"}}, MutatesDB: true, AutoBackup: true}
	techCatalog["engine-cli migrate export"] = cobrahelptree.TechInfo{Summary: "Copy Engine DJ audio and export Rekordbox XML without changing the source", Description: "Runs unattended; writes Contents/, rekordbox.xml, and .sync_state.json under the target. --source defaults to --lib-dir; --target defaults to ./rekordbox. --dry-run reviews every in-scope track as exported or left out with its reason; --plan-token applies only that review. --json prints the HTTP result format."}
}

func newMigrateCommand() *cobra.Command {
	group := &cobra.Command{Use: "migrate", Short: "Import and export DJ application libraries", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	var opts migrate.Options
	var asJSON bool
	c := &cobra.Command{Use: "export", Short: "Copy Engine DJ audio and export Rekordbox XML", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if opts.Source == "" {
				opts.Source = libDirFlag
			}
			if opts.DryRun {
				return printExportPlan(c, opts, asJSON)
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
				_, outputErr = fmt.Fprintf(c.OutOrStdout(), "exported: %d of %d\ncopied: %d\nexisting: %d\nfailed: %d\nunavailable: %d\nleft out: %d\nxml: %s\n", report.Exported, report.Total, report.Copied, report.Existing, report.Failed, report.Unavailable, len(report.LeftOut), displayPath(report.XML))
			}
			return errors.Join(runErr, outputErr)
		}}
	c.Flags().StringVarP(&opts.Source, "source", "s", "", "Source Engine Library directory (empty: --lib-dir)")
	c.Flags().StringVarP(&opts.Target, "target", "t", "", "Rekordbox output directory (empty: ./rekordbox)")
	c.Flags().StringSliceVar(&opts.Playlists, "playlists", nil, "Export only these playlist IDs (folders keep their place; empty: the whole library)")
	c.Flags().StringSliceVar(&opts.Tracks, "tracks", nil, "Export only these track IDs, under the playlist named by --name")
	c.Flags().StringVar(&opts.Name, "name", "", "Playlist name for a --tracks export")
	c.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Review what the export copies and leaves out without writing anything")
	c.Flags().StringVar(&opts.PlanToken, "plan-token", "", "Export only the exact reviewed plan; refused when the library or scope changed")
	c.Flags().BoolVar(&asJSON, "json", false, "Print the migration result as JSON")
	group.AddCommand(c, newRekordboxImportCommand())
	return group
}

type migrationError struct{ error }

func (e migrationError) Error() string { return displayPath(e.error.Error()) }
func (e migrationError) Unwrap() error { return e.error }

func printExportPlan(c *cobra.Command, opts migrate.Options, asJSON bool) error {
	plan, err := migrate.Review(c.Context(), opts)
	if err != nil {
		return migrationError{err}
	}
	out := c.OutOrStdout()
	if asJSON {
		return json.NewEncoder(out).Encode(plan)
	}
	if _, err := fmt.Fprintf(out, "tracks: %d\nexported: %d (%d bytes, %d already present)\nleft out: %d\nplaylists: %d\n", plan.TrackCount, plan.ExportCount, plan.ExportBytes, plan.PresentCount, plan.LeftOutCount, plan.PlaylistCount); err != nil {
		return err
	}
	for _, entry := range plan.Entries {
		if entry.Status == migrate.StatusReady || entry.Status == migrate.StatusPresent {
			continue
		}
		if _, err := fmt.Fprintf(out, "  left out (%s): %s — %s\n", entry.Status, entry.Artist, entry.Title); err != nil {
			return err
		}
	}
	if !plan.CanApply {
		_, err := fmt.Fprintln(out, "Not applicable: nothing in this scope can be exported, or the destination lacks the free space it needs.")
		return err
	}
	_, err = fmt.Fprintf(out, "Plan token: %s\nApply the same options with --plan-token.\n", plan.PlanToken)
	return err
}
