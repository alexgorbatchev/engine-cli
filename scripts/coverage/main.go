// Command coverage collects native test results and enforces statement coverage.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	helptree "github.com/alexgorbatchev/cobra-help-tree/v4"
	"github.com/spf13/cobra"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	root := &cobra.Command{Use: "coverage", Short: "Collect and verify native Go statement coverage", SilenceUsage: true, SilenceErrors: true}
	var output string
	var hostCheck bool
	collectCmd := &cobra.Command{Use: "collect", Short: "Run the host's required tests and retain a verified profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := collect(cmd.Context(), output, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "coverage collection: %s\n", dir)
		if hostCheck {
			return check(cmd.Context(), []string{dir}, true, cmd.OutOrStdout())
		}
		return nil
	}}
	collectCmd.Flags().BoolVar(&hostCheck, "host-check", false, "Report host coverage and require 90% aggregate; native package certification remains separate")
	collectCmd.Flags().StringVar(&output, "output", "", "New output directory (default: a unique .tmp/coverage-* directory)")
	var host bool
	checkCmd := &cobra.Command{Use: "check <directory>...", Short: "Require 90% coverage from matching Linux, macOS and Windows collections", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, dirs []string) error {
		return check(cmd.Context(), dirs, host, cmd.OutOrStdout())
	}}
	checkCmd.Flags().BoolVar(&host, "host", false, "Check host aggregate only; the merged native package gate remains required")
	root.AddCommand(collectCmd, checkCmd)
	if err := helptree.SetupWithOptions(root, helptree.HelpOptions{Catalog: helptree.TechCatalog{"coverage check": {Args: []helptree.ArgSpec{{Name: "<directory>..."}}}}}); err != nil {
		return err
	}
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	return root.ExecuteContext(ctx)
}
