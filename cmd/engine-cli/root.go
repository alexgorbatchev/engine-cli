package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	version      = "1.0.0"
	libDirFlag   string
	noBackupFlag bool
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
		Short: "Manage, audit, repair, synchronize, and back up Engine DJ music collections",
		Long: `engine-cli is a command-line tool designed for DJs to inspect, repair, synchronize,
and safely back up Engine DJ music libraries and album cover art on your computer or external USB drives.`,
		Version: version,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	rootCmd.SetVersionTemplate("{{.Version}}\n")

	rootCmd.PersistentFlags().StringVarP(&libDirFlag, "lib-dir", "l", getDefaultLibraryDir(), "Path to the Engine Library directory")
	rootCmd.PersistentFlags().BoolVar(&noBackupFlag, "no-backup", false, "Disable automatic backup snapshot before modifying database")

	rootCmd.AddCommand(newBackupCommand())
	rootCmd.AddCommand(newRestoreCommand())
	rootCmd.AddCommand(newVerifyCommand())
	rootCmd.AddCommand(newSyncCommand())
	rootCmd.AddCommand(newArtworkCommand())
	rootCmd.AddCommand(newPlaylistCommand())

	setupHelp(rootCmd)

	return rootCmd
}
