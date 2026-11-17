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
				_, writeErr := fmt.Fprintf(out, "operation_id: %s\n", e.ID)
				err = errors.Join(err, writeErr)
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
	var force, dryRun, keepLater bool
	var statePath, planToken string
	var exclude []string
	c := &cobra.Command{Use: "undo [id...]", Short: "Undo a library operation, preserving unrelated later edits", Args: cobra.ArbitraryArgs,
		Long: "Undo the latest operation, or operation IDs from library history; several IDs undo together, newest first, as one operation. Use --dry-run to review what the undo changes, the later edits it meets and a plan token, then apply the same IDs with --plan-token, which keeps those later edits. Conflicts are otherwise refused unless --keep-later or --force is given. Close Engine DJ before modifying the library. Forced undo also retains recovery history.",
		RunE: func(c *cobra.Command, args []string) (err error) {
			if dryRun {
				if planToken != "" {
					return fmt.Errorf("--plan-token applies a reviewed undo; --dry-run returns a new one")
				}
				review, err := history.ReviewUndo(c.Context(), libDirFlag, history.UndoRequest{IDs: args, Exclude: exclude, KeepLater: keepLater, Force: force})
				if err != nil {
					return err
				}
				return printUndoReview(c, review)
			}
			var store *state.Store
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
			e, err := history.Undo(c.Context(), libDirFlag, "", history.UndoOptions{IDs: args, Exclude: exclude, KeepLater: keepLater, PlanToken: planToken, Force: force, Prepare: prepare})
			if err != nil {
				return err
			}
			if store != nil {
				if err = store.RecordUndo(c.Context(), e); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "operation_id: %s\ndry_run: false\n", e.ID)
			return err
		}}
	c.Flags().BoolVar(&force, "force", false, "Restore prior values even when affected records or files have changed")
	c.Flags().StringVar(&statePath, "state-db", "", "Persistent library preferences database")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Review what the undo changes and the later edits it meets, without changing the library")
	c.Flags().BoolVar(&keepLater, "keep-later", false, "Keep records that later edits changed as they are and undo the rest")
	c.Flags().StringVar(&planToken, "plan-token", "", "Apply only if the library still matches the dry run that returned this token")
	c.Flags().StringArrayVar(&exclude, "exclude", nil, "Reviewed entity to keep as it is now, as printed by --dry-run (repeatable)")
	return c
}

// printUndoReview prints an undo's dry run as compact lines.
func printUndoReview(c *cobra.Command, review *history.UndoReview) error {
	out := c.OutOrStdout()
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format, args...)
		return err
	}
	for _, record := range review.Records {
		if err := write("record: %s operation: %s\n", record.ID, record.Operation); err != nil {
			return err
		}
	}
	if err := write("updates: tracks=%d playlists=%d\nrestores: tracks=%d playlists=%d memberships=%d\nremoves: tracks=%d playlists=%d memberships=%d\n",
		review.Updates.Tracks, review.Updates.Playlists, review.Restores.Tracks, review.Restores.Playlists, review.Restores.Memberships,
		review.Removes.Tracks, review.Removes.Playlists, review.Removes.Memberships); err != nil {
		return err
	}
	for _, change := range review.Changes {
		fields := make([]string, 0, len(change.Fields))
		for _, field := range change.Fields {
			fields = append(fields, field.Field)
		}
		if err := write("change: %s action: %s title: %q fields: %s\n", change.Entity, change.Action, change.Title, strings.Join(fields, ",")); err != nil {
			return err
		}
	}
	for _, file := range review.Files {
		if err := write("file: %s action: %s\n", file.Path, file.Action); err != nil {
			return err
		}
	}
	for _, move := range review.Moves {
		if err := write("move: %s -> %s\n", move.From, move.To); err != nil {
			return err
		}
	}
	for _, conflict := range review.Conflicts {
		later := ""
		if conflict.Later != nil {
			later = conflict.Later.ID
		}
		if err := write("conflict: %s kind: %s title: %q later: %s blocking: %t\n", conflict.Entity, conflict.Kind, conflict.Title, later, conflict.Blocking); err != nil {
			return err
		}
	}
	return write("blocked: %t\nplan_token: %s\ndry_run: true\n", review.Blocked, review.PlanToken)
}
