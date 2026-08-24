package main

import (
	"fmt"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/spf13/cobra"
)

func newRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <backup-dir>",
		Short: "Restore your Engine DJ library from a previous backup snapshot",
		Long: `Restore your Engine DJ collection and cover artwork from a previously created backup snapshot.

Automatically saves a safety backup of your current library before restoring unless --no-backup is specified.

Arguments:
  <backup-dir>   Folder path of the backup snapshot to restore. Run 'engine-cli backup --list' to find your backups.`,
		Example: `  engine-cli restore ~/.local/share/engine-cli/backups/backup_20260824_101530
  engine-cli -l "/Volumes/DJ_USB/Engine Library" restore /path/to/backup_directory
  engine-cli --no-backup restore /path/to/backup_directory`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backupDir := args[0]

			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before restore: %w", err)
			}
			if bkInfo != nil {
				if agent.IsAgentMode() {
					cmd.Printf("backup_snapshot: %s\n", bkInfo.Path)
				} else {
					cmd.Printf("Created automated backup snapshot at %s\n", bkInfo.Path)
				}
			}

			if !agent.IsAgentMode() {
				cmd.Printf("Restoring Engine Library from %s into %s...\n", backupDir, libDirFlag)
			}

			err = backup.RestoreBackup(cmd.Context(), backupDir, libDirFlag)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: success")
				cmd.Printf("restored_from: %s\n", backupDir)
				cmd.Printf("target: %s\n", libDirFlag)
				return nil
			}

			cmd.Println("Restore completed successfully! Databases and artwork cache restored.")
			return nil
		},
	}
}
