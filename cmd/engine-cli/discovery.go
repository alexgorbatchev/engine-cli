package main

import (
	"encoding/json"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/discovery"
	"github.com/spf13/cobra"
)

func newLibraryDiscoverCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "discover [path...]",
		Short: "Find Engine DJ libraries on this computer and attached drives",
		Long:  "Find current Engine DJ libraries in Music and attached local drives on macOS and Windows. Optional paths replace automatic locations. Checks each path directly and its Engine Library and Music/Engine Library folders. Does not scan recursively or modify libraries.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			report, err := discovery.Discover(c.Context(), args)
			if err != nil {
				return err
			}
			return printDiscovery(c, report, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print discovered libraries and warnings as JSON")
	return cmd
}

func printDiscovery(c *cobra.Command, r *discovery.Report, asJSON bool) error {
	for i := range r.Libraries {
		r.Libraries[i].Path = displayPath(r.Libraries[i].Path)
		r.Libraries[i].Database = displayPath(r.Libraries[i].Database)
	}
	for i := range r.Warnings {
		r.Warnings[i].Path = displayPath(r.Warnings[i].Path)
	}
	if asJSON {
		return json.NewEncoder(c.OutOrStdout()).Encode(r)
	}
	if agent.IsAgentMode() {
		if _, err := fmt.Fprintf(c.OutOrStdout(), "count: %d\n", len(r.Libraries)); err != nil {
			return err
		}
	} else if len(r.Libraries) == 0 {
		if _, err := fmt.Fprintln(c.OutOrStdout(), "No Engine DJ libraries found."); err != nil {
			return err
		}
	}
	for _, lib := range r.Libraries {
		if _, err := fmt.Fprintf(c.OutOrStdout(), "library: %s | name: %s | source: %s\n", lib.Path, lib.Name, lib.Source); err != nil {
			return err
		}
	}
	for _, warning := range r.Warnings {
		if _, err := fmt.Fprintf(c.ErrOrStderr(), "warning: %s %s\n", warning.Path, warning.Message); err != nil {
			return err
		}
	}
	return nil
}
