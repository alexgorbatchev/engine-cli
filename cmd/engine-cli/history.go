package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/history"
	"github.com/spf13/cobra"
)

func installHistory(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, child := range c.Commands() {
			walk(child)
		}
		if !techCatalog[c.CommandPath()].MutatesDB || c.RunE == nil || c.CommandPath() == root.Name()+" library undo" {
			return
		}
		run := c.RunE
		c.RunE = func(c *cobra.Command, args []string) error {
			operation := strings.ReplaceAll(strings.TrimPrefix(c.CommandPath(), root.Name()+" "), " ", ".")
			var paths []string
			switch operation {
			case "track.add":
				paths = args
			case "track.move":
				if len(args) == 2 {
					paths = []string{args[1]}
				}
			}
			e, err := history.Run(c.Context(), libDirFlag, operation, paths, func(context.Context) error { return run(c, args) })
			if e != nil {
				if _, writeErr := fmt.Fprintf(c.OutOrStdout(), "operation_id: %s\n", e.ID); err == nil {
					err = writeErr
				}
			}
			return err
		}
	}
	walk(root)
}

func newHistoryCommand() *cobra.Command {
	return &cobra.Command{Use: "history", Short: "List reversible library operations and changed entity IDs", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		entries, err := history.List(libDirFlag)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if _, err = fmt.Fprintf(c.OutOrStdout(), "operation: %s command: %s state: %s undo_of: %s\n", e.ID, e.Operation, e.State, e.UndoOf); err != nil {
				return err
			}
			for _, entity := range e.Entities {
				if _, err = fmt.Fprintf(c.OutOrStdout(), "  entity: %s table: %s key: %s\n", entity.ID, entity.Table, entity.Key); err != nil {
					return err
				}
			}
		}
		return nil
	}}
}

func newUndoCommand() *cobra.Command {
	var force, dryRun bool
	c := &cobra.Command{Use: "undo [id]", Short: "Undo a library operation, preserving unrelated later edits", Args: cobra.MaximumNArgs(1),
		Long: "Undo the latest operation, or an operation ID from library history. Conflicts are refused unless --force is given. Use --dry-run to check first. Close Engine DJ before modifying the library. Forced undo also retains recovery history.",
		RunE: func(c *cobra.Command, args []string) error {
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			if !dryRun {
				if _, err := backup.AutoBackupIfNeeded(c.Context(), libDirFlag, noBackupFlag); err != nil {
					return err
				}
			}
			e, err := history.Undo(c.Context(), libDirFlag, id, history.UndoOptions{Force: force, DryRun: dryRun})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "operation_id: %s\ndry_run: %t\n", e.ID, dryRun)
			return err
		}}
	c.Flags().BoolVar(&force, "force", false, "Restore prior values even when affected records or files have changed")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Check undo conflicts without changing the library")
	return c
}
