package main

import (
	"encoding/json"
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/spf13/cobra"
)

var querySystem = systemcheck.Run

func newSystemCommand() *cobra.Command {
	group := &cobra.Command{Use: "system", Short: "Inspect desktop runtime status", Args: cobra.NoArgs}
	var checks []string
	var asJSON bool
	c := &cobra.Command{Use: "check", Short: "Check Engine DJ Desktop status without opening a library", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := systemcheck.Validate(checks); err != nil {
			return err
		}
		r, err := querySystem(c.Context(), checks)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(c.OutOrStdout()).Encode(r)
		}
		v := r.Checks[systemcheck.EngineDesktop]
		running := "unknown"
		if v.Running != nil {
			running = fmt.Sprint(*v.Running)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "check: engine_desktop status: %s running: %s pids: %v\n", v.Status, running, v.PIDs)
		return err
	}}
	c.Flags().StringSliceVar(&checks, "checks", nil, "Checks to run (comma-separated or repeatable); default: engine_desktop")
	c.Flags().BoolVar(&asJSON, "json", false, "Print structured results for UI consumers")
	group.AddCommand(c)
	return group
}

func installMutationGuard(root *cobra.Command) {
	for _, c := range root.Commands() {
		installMutationGuard(c)
	}
	if !techCatalog[root.CommandPath()].MutatesDB || root.RunE == nil {
		return
	}
	run := root.RunE
	root.RunE = func(c *cobra.Command, args []string) error {
		preview := false
		if c.Flags().Lookup("dry-run") != nil {
			var err error
			preview, err = c.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
		}
		if !preview {
			r, err := querySystem(c.Context(), nil)
			if err != nil {
				return err
			}
			if err := systemcheck.AllowMutation(r); err != nil {
				return err
			}
		}
		return run(c, args)
	}
}
