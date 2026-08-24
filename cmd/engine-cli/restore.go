package main

import (
	"fmt"

	"github.com/dj/engine-cli/internal/backup"
	"github.com/spf13/cobra"
)

func newRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <backup-dir>",
		Short: "Restore Engine DJ databases and artwork from a backup snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backupDir := args[0]
			fmt.Printf("Restoring Engine Library from %s into %s...\n", backupDir, libDirFlag)

			err := backup.RestoreBackup(cmd.Context(), backupDir, libDirFlag)
			if err != nil {
				return err
			}

			fmt.Println("Restore completed successfully! Databases and artwork cache restored.")
			return nil
		},
	}
}
