package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func newTrackCommand() *cobra.Command {
	trCmd := &cobra.Command{
		Use:   "track",
		Short: "Manage audio files and collection tracks",
		Long: `Manage audio files and track entries in your master Engine DJ collection.

  - Use 'engine-cli track add <path...>' to import audio files into your collection.
  - Use 'engine-cli track upgrade <id|path>' to fetch replacement audio while keeping your track information.
  - Use 'engine-cli track rm <id|path...>' to remove tracks from your collection.
  - Use 'engine-cli track move <id|path> <dest-path>' to move audio files on disk and update their database paths.
  - Use 'engine-cli track relocate' to batch-fix broken audio file paths across drives or folders.
  - Use 'engine-cli track artwork' to manage embedded cover art.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	trCmd.AddCommand(newDetailsCommand("track"))
	trCmd.AddCommand(newSuggestionCommand())
	trCmd.AddCommand(newTrackAddCommand())
	trCmd.AddCommand(newTrackEditCommand(false))
	batch := &cobra.Command{Use: "batch", Short: "Edit selected tracks together", Args: cobra.NoArgs}
	batch.AddCommand(newTrackEditCommand(true))
	batch.AddCommand(newFindReplaceCommand())
	trCmd.AddCommand(batch)
	trCmd.AddCommand(newTrackUpgradeCommand())
	trCmd.AddCommand(newTrackAnalyzeCommand())
	trCmd.AddCommand(newTrackGridCommand())
	trCmd.AddCommand(newTrackCueCommand())
	trCmd.AddCommand(newTrackRmCommand())
	trCmd.AddCommand(newTrackMoveCommand())
	trCmd.AddCommand(newTrackRelocateCommand())
	trCmd.AddCommand(newTrackArtworkCommand())
	trCmd.AddCommand(newDuplicateCommand())
	trCmd.AddCommand(newTrackCustomTagsCommand())

	return trCmd
}

func newTrackAddCommand() *cobra.Command {
	var toPlaylistFlag string
	var moveFlag bool
	var analyzeFlag bool
	var bpmRangeFlag string

	cmd := &cobra.Command{
		Use:   "add <path...>",
		Short: "Import audio file(s) into your collection",
		Long: `Import one or more audio files into your Engine DJ collection.

Extracts metadata tags (title, artist, album, genre, year, BPM, duration, bitrate) using ffprobe.
Optionally moves the audio file to your Engine Library storage and/or assigns it to a playlist.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.`,
		Example: `  engine-cli track add "/path/to/song.mp3"
  engine-cli track add --to-playlist 2 "/path/to/song.mp3"
  engine-cli track add --move --to-playlist 3 "/Downloads/song1.mp3" "/Downloads/song2.mp3"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before track add: %w", err)
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

			if !analyzeFlag && cmd.Flags().Changed("bpm-range") {
				return fmt.Errorf("choose --analyze to use a BPM range")
			}
			var added []int64
			for _, filePath := range args {
				res, err := track.AddTrack(cmd.Context(), database, libDirFlag, filePath, toPlaylistFlag, moveFlag)
				if err != nil {
					return err
				}

				t := res.Track
				added = append(added, t.ID)
				title := t.Title.String
				if title == "" {
					title = t.Filename.String
				}

				if agent.IsAgentMode() {
					cmd.Println("status: ok")
					cmd.Printf("track_id: %d\n", t.ID)
					cmd.Printf("title: %s\n", title)
					cmd.Printf("path: %s\n", t.PathString())
					if res.Playlist != nil {
						cmd.Printf("playlist_id: %d\n", res.Playlist.ID)
						cmd.Printf("playlist: %s\n", res.Playlist.Title)
					}
					cmd.Printf("moved_file: %t\n", res.Moved)
				} else {
					cmd.Printf("[OK] Added track %q [id:%d]\n", title, t.ID)
					if res.Playlist != nil {
						cmd.Printf("     Assigned to playlist %q [id:%d]\n", res.Playlist.Title, res.Playlist.ID)
					}
					if res.Moved {
						cmd.Printf("     Moved file to %s\n", t.PathString())
					}
				}
			}

			if !analyzeFlag {
				return nil
			}
			report, err := track.AnalyzeImported(cmd.Context(), database, libDirFlag, added, bpmRangeFlag, func(ctx context.Context) error {
				r, err := querySystem(ctx, nil)
				if err != nil {
					return err
				}
				return systemcheck.AllowMutation(r)
			})
			if err != nil {
				return err
			}
			return printAnalysisReport(cmd, report)
		},
	}

	cmd.Flags().StringVar(&toPlaylistFlag, "to-playlist", "", "Assign imported track(s) to the specified playlist (creates if missing)")
	cmd.Flags().BoolVar(&moveFlag, "move", false, "Move audio file(s) into the Engine Library folder")
	cmd.Flags().BoolVar(&analyzeFlag, "analyze", false, "Analyze BPM, musical key, and beat grid of the added tracks")
	cmd.Flags().StringVar(&bpmRangeFlag, "bpm-range", track.DefaultBPMRange, "Tempo range for --analyze: 58-115, 68-135, 78-155, 88-175, or 98-195")

	return cmd
}

func newTrackRmCommand() *cobra.Command {
	var deleteFileFlag bool

	cmd := &cobra.Command{
		Use:   "rm <id|path...>",
		Short: "Remove track(s) from your collection",
		Long: `Remove one or more tracks from your Engine DJ collection and all playlists.

Optionally deletes the physical audio file from disk with --delete-file.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.`,
		Example: `  engine-cli track rm 42
  engine-cli track rm 42 43
  engine-cli track rm "/Music/track.mp3" --delete-file`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before track rm: %w", err)
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

			for _, ident := range args {
				t, err := track.RemoveTrack(cmd.Context(), database, libDirFlag, ident, deleteFileFlag)
				if err != nil {
					return err
				}

				title := t.Title.String
				if title == "" {
					title = t.Filename.String
				}

				if agent.IsAgentMode() {
					cmd.Println("status: ok")
					cmd.Printf("deleted_track_id: %d\n", t.ID)
					cmd.Printf("title: %s\n", title)
					cmd.Printf("deleted_file: %t\n", deleteFileFlag)
				} else {
					cmd.Printf("[OK] Removed track %q [id:%d] from collection\n", title, t.ID)
					if deleteFileFlag {
						cmd.Printf("     Deleted audio file from disk\n")
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&deleteFileFlag, "delete-file", false, "Also delete the physical audio file from disk")

	return cmd
}

func newTrackMoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "move <id|path> <dest-path>",
		Short: "Move an audio file on disk and update its collection path",
		Long: `Move an audio file to a new location on disk and update its path in the Engine DJ collection database.

Arguments:
  <id|path>   The track ID or exact stored path to move.
  <dest-path>    The destination file or directory path.`,
		Example: `  engine-cli track move 42 "/Volumes/DJ_USB/Music/House/song.mp3"
  engine-cli track move "Music/song.mp3" "/Volumes/DJ_USB/Music/House/"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before track move: %w", err)
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

			t, err := track.MoveTrackFile(cmd.Context(), database, libDirFlag, args[0], args[1])
			if err != nil {
				return err
			}

			title := t.Title.String
			if title == "" {
				title = t.Filename.String
			}

			if agent.IsAgentMode() {
				cmd.Println("status: ok")
				cmd.Printf("track_id: %d\n", t.ID)
				cmd.Printf("title: %s\n", title)
				cmd.Printf("new_path: %s\n", t.PathString())
				return nil
			}

			cmd.Printf("[OK] Moved track %q [id:%d] to %s\n", title, t.ID, t.PathString())
			return nil
		},
	}
}

func newTrackRelocateCommand() *cobra.Command {
	var fromFlag, toFlag string

	cmd := &cobra.Command{
		Use:   "relocate",
		Short: "Batch-fix audio file paths across drives or directories",
		Long: `Batch-replace directory path prefixes for tracks in the Engine DJ collection.

Useful when moving your library to a new USB drive, different volume name, or reorganized folder.

Automatically saves a safety backup of your library before making changes unless --no-backup is specified.`,
		Example: `  engine-cli track relocate --from "/Volumes/OldUSB/Music" --to "/Volumes/NewUSB/Music"
  engine-cli track relocate --from "D:\Music" --to "/Volumes/DJ_USB/Music"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromFlag == "" || toFlag == "" {
				return fmt.Errorf("both --from and --to must be specified")
			}

			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before track relocate: %w", err)
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

			count, err := track.RelocateTracks(cmd.Context(), database, fromFlag, toFlag)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: ok")
				cmd.Printf("relocated_count: %d\n", count)
				cmd.Printf("from_prefix: %s\n", fromFlag)
				cmd.Printf("to_prefix: %s\n", toFlag)
				return nil
			}

			cmd.Printf("[OK] Relocated %d track(s) from %q to %q\n", count, fromFlag, toFlag)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromFlag, "from", "", "Old directory prefix to replace")
	cmd.Flags().StringVar(&toFlag, "to", "", "New directory prefix")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")

	return cmd
}

func newTrackArtworkCommand() *cobra.Command {
	artCmd := &cobra.Command{
		Use:   "artwork",
		Short: "Manage and fix embedded track cover artwork",
		Long: `Manage and fix embedded album cover artwork for tracks in your collection.

  - Use 'engine-cli track artwork normalize' to crop wide covers to 1:1 square for DJ jog wheels.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	artCmd.AddCommand(newTrackArtworkNormalizeCommand())

	return artCmd
}

func newTrackArtworkNormalizeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "normalize",
		Short: "Crop album cover art to 1:1 square for DJ jog wheels and player displays",
		Long: `Scan your music collection, extract embedded album cover art, and crop wide or banner images into clean 1:1 square covers so they look crisp on DJ jog wheels and player screens without modifying your audio files.

Automatically saves a safety backup of your library before applying changes unless --no-backup is specified.`,
		Example: `  engine-cli track artwork normalize
  engine-cli -l "/Volumes/DJ_USB/Engine Library" track artwork normalize
  engine-cli --no-backup track artwork normalize`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before artwork normalize: %w", err)
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
				cmd.Println("Normalizing collection artwork to 1:1 square (1400x1400)...")
			}
			report, err := artwork.NormalizeArtwork(cmd.Context(), database, libDirFlag, artCacheDir)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: complete")
				cmd.Printf("total_tracks: %d\n", report.TotalTracks)
				cmd.Printf("normalized: %d\n", report.NormalizedCount)
				cmd.Printf("already_square: %d\n", report.AlreadySquare)
				cmd.Printf("no_art: %d\n", report.NoArtCount)
				cmd.Printf("errors: %d\n", report.ErrorCount)
				return nil
			}

			cmd.Println("Artwork Normalization Complete!")
			cmd.Printf("  Total Tracks:     %d\n", report.TotalTracks)
			cmd.Printf("  Normalized to 1:1:%d\n", report.NormalizedCount)
			cmd.Printf("  Already Square:   %d\n", report.AlreadySquare)
			cmd.Printf("  No Art / Audio:   %d\n", report.NoArtCount)
			if report.ErrorCount > 0 {
				cmd.Printf("  Errors / Skipped: %d\n", report.ErrorCount)
			}

			return nil
		},
	}
}
