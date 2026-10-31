package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/sync"
	"github.com/alexgorbatchev/engine-cli/internal/verify"
	"github.com/spf13/cobra"
)

func newLibraryCommand() *cobra.Command {
	libCmd := &cobra.Command{
		Use:     "library",
		Aliases: []string{"lib", "db"},
		Short:   "Manage, audit, synchronize, and back up Engine DJ databases",
		Long: `Manage, audit, synchronize, and back up Engine DJ databases and library files.

  - Use 'engine-cli library verify' to audit database integrity and missing files.
  - Use 'engine-cli library refresh' to synchronize on-disk metadata tags with database.
  - Use 'engine-cli library backup [path]' to create or list backup snapshots.
  - Use 'engine-cli library restore <path>' to restore from a backup snapshot.
  - Use 'engine-cli library prune' to delete orphaned artwork rows and cache files.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	libCmd.AddCommand(newLibraryVerifyCommand())
	libCmd.AddCommand(newLibraryDiscoverCommand())
	libCmd.AddCommand(acquisitionCommand("library.create", "create <path>", "Create a new Engine library at this path", "path", nil))
	addLibraryLifecycleCommands(libCmd)
	libCmd.AddCommand(newLibraryRegisterCommand(), newLibraryEntitiesCommand())
	libCmd.AddCommand(newLibraryRefreshCommand())
	libCmd.AddCommand(newLibraryTransferCommand())
	libCmd.AddCommand(newLibraryBackupCommand())
	libCmd.AddCommand(newLibraryRestoreCommand())
	libCmd.AddCommand(newLibraryPruneCommand())
	libCmd.AddCommand(newHistoryCommand())
	libCmd.AddCommand(newUndoCommand())
	libCmd.AddCommand(newSmartlistRepairCommand())

	return libCmd
}

func newLibraryVerifyCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check your library for missing songs, broken links, and empty playlists",
		Long: `Perform a read-only audit of your Engine DJ collection to find missing audio files, file size mismatches, broken playlist links, and missing album artwork.

Outputs a clear plain text summary by default or JSON when --json is passed.`,
		Example: `  engine-cli library verify
  engine-cli -l "/Volumes/DJ_USB/Engine Library" library verify
  engine-cli library verify --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath, true)
			if err != nil {
				return err
			}
			defer database.Close()

			report, err := verify.VerifyLibrary(cmd.Context(), database, libDirFlag)
			if err != nil {
				return err
			}

			if jsonOutput {
				jsonBytes, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return fmt.Errorf("formatting JSON verification report: %w", err)
				}
				cmd.Println(string(jsonBytes))
				return nil
			}

			statusStr := "healthy"
			if !report.IsHealthy {
				statusStr = "issues_detected"
			}

			if agent.IsAgentMode() {
				cmd.Printf("total_tracks: %d\n", report.TotalTracks)
				cmd.Printf("missing_files: %d\n", report.MissingOnDiskCount)
				cmd.Printf("corrupt_sizes: %d\n", report.BytesMismatchCount)
				cmd.Printf("broken_entities: %d\n", report.OrphanedCount)
				cmd.Printf("missing_artwork: %d\n", report.MissingArtCount)
				cmd.Printf("empty_playlists: %d\n", report.EmptyPlaylistCount)
				cmd.Printf("status: %s\n", statusStr)
				return nil
			}

			cmd.Println("Library Verification Report")
			cmd.Printf("  Total Tracks Audited:   %d\n", report.TotalTracks)
			cmd.Printf("  Missing Audio Files:    %d\n", report.MissingOnDiskCount)
			cmd.Printf("  Size Mismatches:        %d\n", report.BytesMismatchCount)
			cmd.Printf("  Broken Playlist Links:  %d\n", report.OrphanedCount)
			cmd.Printf("  Missing Album Covers:   %d\n", report.MissingArtCount)
			cmd.Printf("  Empty Playlists:        %d\n", report.EmptyPlaylistCount)
			cmd.Printf("  Overall Health Status:  %s\n", statusStr)

			if len(report.Issues) > 0 {
				cmd.Println("\nDetected Issues:")
				for _, iss := range report.Issues {
					cmd.Printf("  [%s] %s\n", iss.Type, iss.Description)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output verification report in JSON format")

	return cmd
}

func newLibraryRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Update song information in Engine DJ from audio files on disk",
		Long: `Scan your audio files and update song titles, artists, BPM, and file sizes in your Engine DJ collection if you modified them outside Engine DJ. Also finds and lists any new songs on disk that are not yet added to your collection.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.`,
		Example: `  engine-cli library refresh
  engine-cli -l "/Volumes/DJ_USB/Engine Library" library refresh
  engine-cli --no-backup library refresh`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before sync: %w", err)
			}
			if bkInfo != nil {
				if agent.IsAgentMode() {
					cmd.Printf("backup_snapshot: %s\n", bkInfo.Path)
				} else {
					cmd.Printf("Created automated backup snapshot at %s\n", bkInfo.Path)
				}
			}

			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			if !agent.IsAgentMode() {
				cmd.Println("Scanning on-disk audio files and synchronizing tags with database...")
			}
			report, refreshErr := sync.SyncLibrary(cmd.Context(), database, libDirFlag)
			if report == nil {
				return refreshErr
			}

			if agent.IsAgentMode() {
				if refreshErr != nil {
					cmd.Println("status: incomplete")
					cmd.Printf("error_code: %s\n", progress.ErrorCode(refreshErr))
				} else {
					cmd.Println("status: complete")
				}
				cmd.Printf("total_tracks: %d\n", report.TotalDBTracks)
				cmd.Printf("updated_records: %d\n", report.UpdatedCount)
				cmd.Printf("missing_files: %d\n", report.MissingCount)
				cmd.Printf("unindexed_files: %d\n", report.UnindexedCount)
				for _, p := range report.UnindexedPaths {
					cmd.Printf("unindexed: %s\n", p)
				}
				return refreshErr
			}

			if refreshErr != nil {
				cmd.Println("Refresh stopped before completion. Review the partial counts below; use library undo to revert saved updates.")
			} else {
				cmd.Println("Library Sync Complete!")
			}
			cmd.Printf("  Total Tracks in DB:   %d\n", report.TotalDBTracks)
			cmd.Printf("  Updated Records:      %d\n", report.UpdatedCount)
			cmd.Printf("  Missing Files:        %d\n", report.MissingCount)
			cmd.Printf("  Unindexed Disk Files: %d\n", report.UnindexedCount)

			if len(report.UnindexedPaths) > 0 {
				cmd.Println("\nUnindexed audio files found on disk:")
				for idx, p := range report.UnindexedPaths {
					if idx < 15 {
						cmd.Printf("  - %s\n", p)
					}
				}
				if len(report.UnindexedPaths) > 15 {
					cmd.Printf("  ... and %d more files\n", len(report.UnindexedPaths)-15)
				}
			}

			return refreshErr
		},
	}
}

func newLibraryBackupCommand() *cobra.Command {
	var listBackups bool
	var outDirFlag string

	cmd := &cobra.Command{
		Use:   "backup [path]",
		Short: "Create a safety backup snapshot of your Engine DJ library",
		Long: `Create a timestamped backup snapshot of your Engine DJ SQLite databases (Database2/m.db, p.db) and Artwork/ thumbnail cache.

Includes committed changes even while databases are open. Each database is captured independently.
Close Engine DJ if you need matching state across all database files and artwork.

By default, backups are saved in <lib-dir>/.engine-cli/backups, or to an optional [path] specified as an argument or via -o.`,
		Example: `  engine-cli library backup
  engine-cli library backup "/Volumes/BackupDrive/EngineBackups"
  engine-cli library backup --list`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			destDir := outDirFlag
			if len(args) > 0 && args[0] != "" {
				destDir = args[0]
			}
			if destDir == "" {
				var err error
				destDir, err = backup.GetDefaultBackupDir(libDirFlag)
				if err != nil {
					return err
				}
			}

			if listBackups {
				backups, err := backup.ListBackups(libDirFlag, destDir)
				if err != nil {
					return err
				}
				if len(backups) == 0 {
					if agent.IsAgentMode() {
						cmd.Println("count: 0")
						return nil
					}
					cmd.Printf("No backups found in %s\n", destDir)
					return nil
				}

				if agent.IsAgentMode() {
					cmd.Printf("count: %d\n", len(backups))
					for _, b := range backups {
						cmd.Printf("backup: %s | timestamp: %s | size_bytes: %d\n", b.Path, b.Timestamp.Format("2006-01-02T15:04:05Z"), b.SizeBytes)
					}
					return nil
				}

				cmd.Printf("Available Backups in %s:\n", destDir)
				for _, b := range backups {
					cmd.Printf("  - %s (%s, %d bytes)\n", b.Path, b.Timestamp.Format("2006-01-02 15:04:05"), b.SizeBytes)
				}
				return nil
			}

			if !agent.IsAgentMode() {
				cmd.Printf("Creating backup snapshot from %s...\n", libDirFlag)
			}
			info, err := backup.CreateBackup(cmd.Context(), libDirFlag, destDir)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: success")
				cmd.Printf("path: %s\n", info.Path)
				cmd.Printf("size_bytes: %d\n", info.SizeBytes)
				cmd.Printf("db_files: %d\n", info.DBFiles)
				cmd.Printf("art_files: %d\n", info.ArtFiles)
				return nil
			}

			cmd.Println("Backup Created Successfully!")
			cmd.Printf("  Snapshot Location: %s\n", info.Path)
			cmd.Printf("  Total Size:        %d bytes\n", info.SizeBytes)
			cmd.Printf("  Database Files:    %d\n", info.DBFiles)
			cmd.Printf("  Artwork Files:     %d\n", info.ArtFiles)

			return nil
		},
	}

	cmd.Flags().BoolVar(&listBackups, "list", false, "List available backup snapshots without creating a new backup")
	cmd.Flags().StringVarP(&outDirFlag, "out-dir", "o", "", "Destination directory for backup snapshots (default: <lib-dir>/.engine-cli/backups)")

	return cmd
}

func newLibraryRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <path>",
		Short: "Restore your Engine DJ library from a previous backup snapshot",
		Long: `Restore your Engine DJ SQLite databases and Artwork/ cache from a previous backup snapshot directory.

Close Engine DJ before restoring.

Arguments:
  <path>   The full path to the backup snapshot folder. Use 'engine-cli library backup --list' to find snapshots.`,
		Example: `  engine-cli library restore <lib-dir>/.engine-cli/backups/backup-2026-08-23T20-30-00Z
  engine-cli -l "/Volumes/DJ_USB/Engine Library" library restore "/Backups/backup-2026-08-23T20-30-00Z"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			snapshotPath := args[0]
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic safety backup before restore: %w", err)
			}
			if bkInfo != nil {
				if agent.IsAgentMode() {
					cmd.Printf("safety_backup: %s\n", bkInfo.Path)
				} else {
					cmd.Printf("Saved pre-restore safety backup to %s\n", bkInfo.Path)
				}
			}

			if !agent.IsAgentMode() {
				cmd.Printf("Restoring library from %s to %s...\n", snapshotPath, libDirFlag)
			}

			err = backup.RestoreBackup(cmd.Context(), snapshotPath, libDirFlag)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: success")
				cmd.Printf("restored_from: %s\n", snapshotPath)
				cmd.Printf("target_dir: %s\n", libDirFlag)
				return nil
			}

			cmd.Println("Library Restored Successfully!")
			cmd.Printf("  Restored From: %s\n", snapshotPath)
			cmd.Printf("  Target Library: %s\n", libDirFlag)

			return nil
		},
	}
}

func newLibraryPruneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Clean up unused cover art and reclaim disk space",
		Long: `Scan your collection to find and delete old, unneeded cover art records and cached thumbnail files that are no longer used by any songs in your library.

Automatically saves a safety backup of your library before applying changes unless --no-backup is specified.`,
		Example: `  engine-cli library prune
  engine-cli -l "/Volumes/DJ_USB/Engine Library" library prune
  engine-cli --no-backup library prune`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before library prune: %w", err)
			}
			if bkInfo != nil {
				if agent.IsAgentMode() {
					cmd.Printf("backup_snapshot: %s\n", bkInfo.Path)
				} else {
					cmd.Printf("Created automated backup snapshot at %s\n", bkInfo.Path)
				}
			}

			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			artCacheDir := filepath.Join(libDirFlag, "Artwork")

			if !agent.IsAgentMode() {
				cmd.Println("Pruning orphaned album artwork records and stale cache files...")
			}
			report, err := artwork.PruneArtwork(cmd.Context(), database, artCacheDir)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: complete")
				cmd.Printf("deleted_db_rows: %d\n", report.DeletedDBRows)
				cmd.Printf("deleted_disk_files: %d\n", report.DeletedDiskFiles)
				return nil
			}

			cmd.Println("Library Prune Complete!")
			cmd.Printf("  Deleted DB Rows:    %d\n", report.DeletedDBRows)
			cmd.Printf("  Deleted Disk Files: %d\n", report.DeletedDiskFiles)

			return nil
		},
	}
}
