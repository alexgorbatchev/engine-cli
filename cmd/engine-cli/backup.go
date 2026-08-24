package main

import (
	"fmt"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/spf13/cobra"
)

func newBackupCommand() *cobra.Command {
	var outDir string
	var listOnly bool

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a safety backup snapshot of your Engine DJ library",
		Long: `Create a complete safety backup snapshot of your Engine DJ music library and cover art.

By default, backups are saved to your user data folder ($XDG_DATA_HOME/engine-cli/backups or ~/.local/share/engine-cli/backups).
Use --out-dir (-o) to specify an alternate destination folder.
Use --list to view your existing backups.`,
		Example: `  engine-cli backup
  engine-cli backup --list
  engine-cli backup -o /path/to/custom/backups
  engine-cli -l "/Volumes/DJ_USB/Engine Library" backup`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listOnly {
				targetDir := outDir
				if targetDir == "" {
					defaultDir, err := backup.GetDefaultBackupDir()
					if err != nil {
						return fmt.Errorf("resolving default backup directory: %w", err)
					}
					targetDir = defaultDir
				}
				backups, err := backup.ListBackups(targetDir)
				if err != nil {
					return err
				}
				if len(backups) == 0 {
					if agent.IsAgentMode() {
						cmd.Println("count: 0")
						return nil
					}
					cmd.Println("No backups found in", targetDir)
					return nil
				}

				if agent.IsAgentMode() {
					cmd.Printf("count: %d\n", len(backups))
					for _, b := range backups {
						cmd.Printf("backup: %s path=%s timestamp=%s db_files=%d art_files=%d size_bytes=%d\n",
							b.Name, b.Path, b.Timestamp.Format("2006-01-02T15:04:05Z07:00"), b.DBFiles, b.ArtFiles, b.SizeBytes)
					}
					return nil
				}

				cmd.Printf("Found %d backups in %s:\n\n", len(backups), targetDir)
				for _, b := range backups {
					cmd.Printf("  - %s (%s) [DBs: %d, Art: %d, Size: %.2f MB]\n",
						b.Name, b.Timestamp.Format("2006-01-02 15:04:05"), b.DBFiles, b.ArtFiles, float64(b.SizeBytes)/(1024*1024))
				}
				return nil
			}

			if !agent.IsAgentMode() {
				cmd.Printf("Creating backup of Engine Library at %s...\n", libDirFlag)
			}
			info, err := backup.CreateBackup(cmd.Context(), libDirFlag, outDir)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: success")
				cmd.Printf("path: %s\n", info.Path)
				cmd.Printf("name: %s\n", info.Name)
				cmd.Printf("db_files: %d\n", info.DBFiles)
				cmd.Printf("art_files: %d\n", info.ArtFiles)
				cmd.Printf("size_bytes: %d\n", info.SizeBytes)
				return nil
			}

			cmd.Println("Backup created successfully!")
			cmd.Printf("  Path:      %s\n", info.Path)
			cmd.Printf("  DB Files:  %d\n", info.DBFiles)
			cmd.Printf("  Art Files: %d\n", info.ArtFiles)
			cmd.Printf("  Size:      %.2f MB\n", float64(info.SizeBytes)/(1024*1024))

			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "out-dir", "o", "", "Destination directory for backup snapshots (default: $XDG_DATA_HOME/engine-cli/backups)")
	cmd.Flags().BoolVar(&listOnly, "list", false, "List available backup snapshots without creating a new backup")

	return cmd
}
