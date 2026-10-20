package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/spf13/cobra"
)

func newStagingCommand() *cobra.Command {
	group := &cobra.Command{Use: "staging", Short: "Review downloaded music before importing it", Args: cobra.NoArgs}
	group.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
	for _, action := range []string{"list", "inspect", "rm", "import"} {
		var asJSON bool
		var playlist string
		use := action
		args := cobra.NoArgs
		switch action {
		case "inspect":
			use += " <id>"
			args = cobra.ExactArgs(1)
		case "rm", "import":
			use += " <ids...>"
			args = cobra.MinimumNArgs(1)
		}
		descriptions := map[string]string{"list": "List staged tracks and mixes", "inspect": "Inspect a staged track or mix", "rm": "Remove staged tracks or mixes", "import": "Import selected staged tracks into your collection"}
		cmd := &cobra.Command{Use: use, Short: descriptions[action], Args: args, RunE: func(c *cobra.Command, args []string) error {
			store := staging.New(libDirFlag)
			var result any
			run := func(ctx context.Context) error {
				var err error
				switch action {
				case "list":
					result, err = store.List(ctx)
				case "inspect":
					result, err = store.Inspect(ctx, args[0])
				case "rm":
					var removed []string
					removed, err = store.Remove(ctx, args)
					result = map[string]any{"removed": removed}
				case "import":
					if _, err = backup.AutoBackupIfNeeded(ctx, libDirFlag, noBackupFlag); err != nil {
						return err
					}
					path := filepath.Join(libDirFlag, "Database2", "m.db")
					if _, err := os.Stat(path); err != nil {
						return err
					}
					d, openErr := db.Open(path)
					if openErr != nil {
						return openErr
					}
					defer d.Close()
					result, err = store.Import(ctx, d, staging.ImportOptions{IDs: args, ToPlaylist: playlist})
				}
				return err
			}
			if action == "rm" {
				_, err := history.Run(c.Context(), libDirFlag, "staging.rm", nil, run)
				if err != nil {
					return err
				}
			} else if err := run(c.Context()); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(c.OutOrStdout()).Encode(result)
			}
			switch v := result.(type) {
			case []staging.Item:
				for _, item := range v {
					if _, err := fmt.Fprintf(c.OutOrStdout(), "%s [id:%s kind:%s status:%s]\n", item.Title, item.ID, item.Kind, item.Status); err != nil {
						return err
					}
				}
			case *staging.Item:
				_, err := fmt.Fprintf(c.OutOrStdout(), "%s [id:%s kind:%s status:%s]\n", v.Title, v.ID, v.Kind, v.Status)
				return err
			default:
				return json.NewEncoder(c.OutOrStdout()).Encode(result)
			}
			return nil
		}}
		cmd.Flags().BoolVar(&asJSON, "json", false, "Print structured results")
		if action == "import" {
			cmd.Flags().StringVar(&playlist, "to-playlist", "", "Assign imported tracks to an existing playlist ID")
		}
		group.AddCommand(cmd)
	}
	return group
}
