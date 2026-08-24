package main

import (
	"fmt"

	"github.com/dj/engine-cli/internal/backup"
	"github.com/spf13/cobra"
)

func newBackupCommand() *cobra.Command {
	var outDir string
	var listOnly bool

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create an atomic snapshot of Engine DJ SQLite databases and Artwork cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			if listOnly {
				targetDir := outDir
				if targetDir == "" {
					targetDir = libDirFlag + "/Database2/Backups"
				}
				backups, err := backup.ListBackups(targetDir)
				if err != nil {
					return err
				}
				if len(backups) == 0 {
					fmt.Println("No backups found in", targetDir)
					return nil
				}
				fmt.Printf("Found %d backups in %s:\n\n", len(backups), targetDir)
				for _, b := range backups {
					fmt.Printf("  - %s (%s) [DBs: %d, Art: %d, Size: %.2f MB]\n",
						b.Name, b.Timestamp.Format("2006-01-02 15:04:05"), b.DBFiles, b.ArtFiles, float64(b.SizeBytes)/(1024*1024))
				}
				return nil
			}

			fmt.Printf("Creating backup of Engine Library at %s...\n", libDirFlag)
			info, err := backup.CreateBackup(cmd.Context(), libDirFlag, outDir)
			if err != nil {
				return err
			}

			fmt.Println("Backup created successfully!")
			fmt.Printf("  Path:      %s\n", info.Path)
			fmt.Printf("  DB Files:  %d\n", info.DBFiles)
			fmt.Printf("  Art Files: %d\n", info.ArtFiles)
			fmt.Printf("  Size:      %.2f MB\n", float64(info.SizeBytes)/(1024*1024))

			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "out-dir", "o", "", "Destination directory for backup snapshots (default: <lib-dir>/Database2/Backups)")
	cmd.Flags().BoolVar(&listOnly, "list", false, "List available backup snapshots without creating a new backup")

	return cmd
}
