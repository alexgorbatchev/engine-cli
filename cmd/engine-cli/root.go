package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	version    = "1.0.0"
	libDirFlag string
)

func getDefaultLibraryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "Engine Library"
	}
	return filepath.Join(home, "Music", "Engine Library")
}

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "engine-cli",
		Short: "CLI tool for managing, backing up, verifying, and repairing Engine DJ libraries and SQLite databases.",
		Long: `engine-cli is a powerful utility designed for DJs to manage Engine DJ collection databases (m.db),
perform integrity audits, normalize non-square cover artwork, synchronize on-disk metadata tags,
and create atomic backups of Engine DJ libraries.`,
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	rootCmd.SetVersionTemplate("{{.Version}}\n")

	rootCmd.PersistentFlags().StringVarP(&libDirFlag, "lib-dir", "l", getDefaultLibraryDir(), "Path to the Engine Library directory")

	rootCmd.AddCommand(newBackupCommand())
	rootCmd.AddCommand(newRestoreCommand())
	rootCmd.AddCommand(newVerifyCommand())
	rootCmd.AddCommand(newSyncCommand())
	rootCmd.AddCommand(newArtworkCommand())
	rootCmd.AddCommand(newPlaylistCommand())

	return rootCmd
}
