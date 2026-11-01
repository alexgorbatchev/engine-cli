// Maintainer-only synthetic fixture tool. Not part of the public Engine CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := command().ExecuteContext(ctx); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func command() *cobra.Command {
	var root, inputPath, name, expected string
	cmd := &cobra.Command{Use: "desktop-repair-handoff", SilenceUsage: true, SilenceErrors: true}
	cmd.PersistentFlags().StringVar(&root, "root", "", "Absolute fresh or relocated synthetic fixture root")
	cmd.PersistentFlags().StringVar(&inputPath, "inputs", "", "Reviewed committed-source, golden and binary bindings")
	cmd.AddCommand(&cobra.Command{Use: "seed", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		in, g, err := loadInputs(c.Context(), inputPath, root)
		if err != nil {
			return err
		}
		return seed(c.Context(), root, in, g)
	}})
	v := &cobra.Command{Use: "verify", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		in, g, err := loadInputs(c.Context(), inputPath, root)
		if err != nil {
			return err
		}
		return verify(c.Context(), root, in, g, name, expected)
	}}
	v.Flags().StringVar(&name, "name", "", "One synthetic case; empty verifies all four")
	v.Flags().StringVar(&expected, "expect", "seed", "seed, repaired, repair-undone or original-deletion-undone")
	cmd.AddCommand(v)
	return cmd
}
