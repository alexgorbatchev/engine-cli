package main

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/spf13/cobra"
)

func newLibraryRegisterCommand() *cobra.Command {
	var newCopy, asJSON bool
	c := &cobra.Command{Use: "register", Short: "Register a library for stable references and syncing", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if _, err := backup.AutoBackupIfNeeded(c.Context(), libDirFlag, noBackupFlag); err != nil {
			return err
		}
		return readDuplicates(func(d *sql.DB) error {
			if err := history.Preserve(c.Context(), identity.Path(libDirFlag)); err != nil {
				return err
			}
			r, err := identity.Register(c.Context(), d, libDirFlag, newCopy)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(c.OutOrStdout()).Encode(r)
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "library_id: %s\n", r.ID)
			return err
		})
	}}
	c.Flags().BoolVar(&newCopy, "new-copy", false, "Assign this copied library a separate instance identity")
	c.Flags().BoolVar(&asJSON, "json", false, "Return registration as JSON")
	return c
}

func newLibraryEntitiesCommand() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "list", Short: "List stable track and playlist references", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return readDuplicates(func(d *sql.DB) error {
			items, err := identity.List(c.Context(), d, libDirFlag)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(c.OutOrStdout()).Encode(items)
			}
			for _, item := range items {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "kind: %s id: %d ref: %s\n", item.Kind, item.ID, item.Ref); err != nil {
					return err
				}
			}
			return nil
		})
	}}
	c.Flags().BoolVar(&asJSON, "json", false, "Return entity references as JSON")
	group := &cobra.Command{Use: "entity", Short: "Read stable library entity references", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	group.AddCommand(c)
	return group
}
