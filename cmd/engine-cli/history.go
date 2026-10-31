package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/state"
	"github.com/spf13/cobra"
)

func installHistory(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, child := range c.Commands() {
			walk(child)
		}
		if c.Annotations["engine-api-operation"] == "true" || !techCatalog[c.CommandPath()].MutatesDB || c.RunE == nil || c.CommandPath() == root.Name()+" library undo" || c.CommandPath() == root.Name()+" library sync" {
			return
		}
		run := c.RunE
		c.RunE = func(c *cobra.Command, args []string) (err error) {
			operation := strings.ReplaceAll(strings.TrimPrefix(c.CommandPath(), root.Name()+" "), " ", ".")
			if operation == "library.restore" {
				lease, acquireErr := intakelease.Replace(c.Context(), libDirFlag)
				if acquireErr != nil {
					return acquireErr
				}
				defer func() { err = errors.Join(err, lease.Close()) }()
			}
			options := history.RunOptions{Operation: operation, Scope: history.ScopeCollection,
				RepairSmartlists: operation != "library.create" && operation != "library.restore"}
			e, err := history.RunPrepared(c.Context(), libDirFlag, options, func(ctx context.Context) error {
				previous := c.Context()
				c.SetContext(ctx)
				defer c.SetContext(previous)
				return run(c, args)
			})
			if e != nil {
				out := c.OutOrStdout()
				if flag := c.Flags().Lookup("json"); flag != nil && flag.Value.String() == "true" {
					out = c.ErrOrStderr()
				}
				if _, writeErr := fmt.Fprintf(out, "operation_id: %s\n", e.ID); err == nil {
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
	var statePath string
	c := &cobra.Command{Use: "undo [id]", Short: "Undo a library operation, preserving unrelated later edits", Args: cobra.MaximumNArgs(1),
		Long: "Undo the latest operation, or an operation ID from library history. Conflicts are refused unless --force is given. Use --dry-run to check first. Close Engine DJ before modifying the library. Forced undo also retains recovery history.",
		RunE: func(c *cobra.Command, args []string) (err error) {
			var store *state.Store
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			prepare := func(ctx context.Context) error {
				var err error
				if statePath == "" {
					statePath, err = state.DefaultPath()
					if err != nil {
						return err
					}
				}
				store, err = state.Open(statePath)
				if err != nil {
					return err
				}
				_, err = backup.AutoBackupIfNeeded(ctx, libDirFlag, noBackupFlag)
				return err
			}
			defer func() {
				if store != nil {
					err = errors.Join(err, store.Close())
				}
			}()
			e, err := history.Undo(c.Context(), libDirFlag, id, history.UndoOptions{Force: force, DryRun: dryRun, Prepare: prepare})
			if err != nil {
				return err
			}
			if store != nil {
				if err = store.RecordUndo(c.Context(), e); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "operation_id: %s\ndry_run: %t\n", e.ID, dryRun)
			return err
		}}
	c.Flags().BoolVar(&force, "force", false, "Restore prior values even when affected records or files have changed")
	c.Flags().StringVar(&statePath, "state-db", "", "Persistent library preferences database")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Check undo conflicts without changing the library")
	return c
}
