package main

import (
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/playlist"
	"github.com/spf13/cobra"
)

func newPlaylistCommand() *cobra.Command {
	plCmd := &cobra.Command{
		Use:   "playlist",
		Short: "Inspect and manage playlists and folders",
		Long: `Inspect and manage playlists and folders in your Engine DJ collection.

  - Use 'engine-cli playlist list' to view the full folder and playlist tree.
  - Use 'engine-cli playlist inspect <pl|id>' to view the songs inside a playlist.
  - Use 'engine-cli playlist create <name>' to create a playlist or folder.
  - Use 'engine-cli playlist rm <pl|id>' to remove a playlist.
  - Use 'engine-cli playlist move <pl|id>' to move a playlist under a parent folder.
  - Use 'engine-cli playlist track' to manage song memberships inside playlists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	plCmd.AddCommand(newPlaylistListCommand())
	plCmd.AddCommand(newPlaylistInspectCommand())
	plCmd.AddCommand(newPlaylistCreateCommand())
	plCmd.AddCommand(newPlaylistRmCommand())
	plCmd.AddCommand(newPlaylistMoveCommand())
	plCmd.AddCommand(newPlaylistTrackCommand())

	return plCmd
}

func newPlaylistListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Display all playlists and folders in your library",
		Long: `Display the full folder and playlist tree of your Engine DJ collection with song counts, starting from Collection.

In standard mode, displays a clean ASCII tree.
In AGENT=1 mode, displays a compact token-conservative bulleted list.`,
		Example: `  engine-cli playlist list
  engine-cli -l "/Volumes/DJ_USB/Engine Library" playlist list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath, true)
			if err != nil {
				return err
			}
			defer database.Close()

			tree, err := playlist.GetPlaylistHierarchy(cmd.Context(), database)
			if err != nil {
				return err
			}

			if len(tree) == 0 {
				if agent.IsAgentMode() {
					cmd.Println("status: empty")
					return nil
				}
				cmd.Println("No playlists found in database.")
				return nil
			}

			if agent.IsAgentMode() {
				cmd.Print(playlist.FormatPlaylistBulletList(tree))
				return nil
			}

			cmd.Print(playlist.FormatPlaylistTree(tree))
			return nil
		},
	}
}

func newPlaylistInspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <pl|id>",
		Short: "Display all songs inside a playlist",
		Long: `Display all songs and track lengths inside a specific playlist.

Arguments:
  <pl|id>   The name or numeric ID of the playlist (e.g. "Tech House" or 2). Run 'engine-cli playlist list' to see all playlists.`,
		Example: `  engine-cli playlist inspect 2
  engine-cli playlist inspect "Tech House"
  engine-cli -l "/Volumes/DJ_USB/Engine Library" playlist inspect 43`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath, true)
			if err != nil {
				return err
			}
			defer database.Close()

			details, err := playlist.InspectPlaylist(cmd.Context(), database, args[0])
			if err != nil {
				return err
			}

			if len(details.Tracks) == 0 {
				if agent.IsAgentMode() {
					cmd.Println("status: empty")
					return nil
				}
				cmd.Println("No tracks in playlist.")
				return nil
			}

			if agent.IsAgentMode() {
				for _, t := range details.Tracks {
					cmd.Printf("- %s\n", playlist.FormatTrackLine(t))
				}
				return nil
			}

			for _, t := range details.Tracks {
				cmd.Println(playlist.FormatTrackLine(t))
			}

			return nil
		},
	}
}

func newPlaylistCreateCommand() *cobra.Command {
	var parentFlag string
	var folderFlag bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new playlist or folder",
		Long: `Create a new playlist or folder in your Engine DJ collection hierarchy.

Arguments:
  <name>   The title for the new playlist or folder.`,
		Example: `  engine-cli playlist create "Deep House"
  engine-cli playlist create "Afro House" --parent "House"
  engine-cli playlist create "Genres" --folder`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before playlist create: %w", err)
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

			created, err := playlist.CreatePlaylist(cmd.Context(), database, args[0], parentFlag, folderFlag)
			if err != nil {
				return err
			}

			kind := "playlist"
			if folderFlag {
				kind = "folder"
			}

			if agent.IsAgentMode() {
				cmd.Println("status: ok")
				cmd.Printf("id: %d\n", created.ID)
				cmd.Printf("title: %s\n", created.Title)
				cmd.Printf("parent_id: %d\n", created.ParentListID)
				cmd.Printf("is_folder: %t\n", created.IsFolder)
				return nil
			}

			cmd.Printf("[OK] Created %s %q [id:%d]\n", kind, created.Title, created.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&parentFlag, "parent", "p", "", "Parent playlist or folder name or ID")
	cmd.Flags().BoolVar(&folderFlag, "folder", false, "Create a folder container instead of a regular playlist")

	return cmd
}

func newPlaylistRmCommand() *cobra.Command {
	var forceFlag bool

	cmd := &cobra.Command{
		Use:   "rm <pl|id>",
		Short: "Delete a playlist or folder",
		Long: `Delete a playlist or folder from your Engine DJ collection.

Songs inside the playlist will remain in your master collection.

Arguments:
  <pl|id>   The name or numeric ID of the playlist to delete.`,
		Example: `  engine-cli playlist rm "Old Playlist"
  engine-cli playlist rm 14 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before playlist rm: %w", err)
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

			deleted, err := playlist.DeletePlaylist(cmd.Context(), database, args[0], forceFlag)
			if err != nil {
				return err
			}

			if agent.IsAgentMode() {
				cmd.Println("status: ok")
				cmd.Printf("deleted_id: %d\n", deleted.ID)
				cmd.Printf("deleted_title: %s\n", deleted.Title)
				return nil
			}

			cmd.Printf("[OK] Deleted playlist %q [id:%d]\n", deleted.Title, deleted.ID)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Recursively delete folder and all nested child playlists")

	return cmd
}

func newPlaylistMoveCommand() *cobra.Command {
	var toFlag string

	cmd := &cobra.Command{
		Use:   "move <pl|id>",
		Short: "Relocate a playlist or folder under a parent folder",
		Long: `Move a playlist or folder under a new parent folder in your Engine DJ collection hierarchy.

Arguments:
  <pl|id>   The name or numeric ID of the playlist to move.`,
		Example: `  engine-cli playlist move "Deep House" --to "House"
  engine-cli playlist move 15 --to 0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if toFlag == "" {
				return fmt.Errorf("destination parent must be specified via --to <parent>")
			}

			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before playlist move: %w", err)
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

			target, parent, err := playlist.MovePlaylist(cmd.Context(), database, args[0], toFlag)
			if err != nil {
				return err
			}

			parentName := "Collection"
			if parent != nil {
				parentName = parent.Title
			}

			if agent.IsAgentMode() {
				cmd.Println("status: ok")
				cmd.Printf("id: %d\n", target.ID)
				cmd.Printf("title: %s\n", target.Title)
				cmd.Printf("parent_id: %d\n", target.ParentListID)
				return nil
			}

			cmd.Printf("[OK] Moved playlist %q [id:%d] under %q\n", target.Title, target.ID, parentName)
			return nil
		},
	}

	cmd.Flags().StringVar(&toFlag, "to", "", "Destination parent folder name, ID, or 'Collection' (root)")
	_ = cmd.MarkFlagRequired("to")

	return cmd
}

func newPlaylistTrackCommand() *cobra.Command {
	ptCmd := &cobra.Command{
		Use:   "track",
		Short: "Manage track memberships inside playlists",
		Long: `Manage track memberships inside Engine DJ playlists.

  - Use 'engine-cli playlist track add <pl|id> <tr|id|path...>' to add tracks to a playlist.
  - Use 'engine-cli playlist track rm <pl|id> <tr|id|path...>' to remove tracks from a playlist.
  - Use 'engine-cli playlist track move <src-pl|id> <dst-pl|id> <tr|id|path...>' to move tracks between playlists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	ptCmd.AddCommand(newPlaylistTrackAddCommand())
	ptCmd.AddCommand(newPlaylistTrackRmCommand())
	ptCmd.AddCommand(newPlaylistTrackMoveCommand())

	return ptCmd
}

func newPlaylistTrackAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add <pl|id> <tr|id|path...>",
		Short: "Add track(s) to a playlist",
		Long: `Add one or more tracks to a playlist.

Arguments:
  <pl|id>           The name or numeric ID of the playlist.
  <tr|id|path...>   One or more track titles, IDs, or file paths already in the collection.`,
		Example: `  engine-cli playlist track add "Tech House" 42
  engine-cli playlist track add "House" "Song Title" "Another Song"`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before playlist track add: %w", err)
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

			plIdentifier := args[0]
			for _, trackIdent := range args[1:] {
				pl, tr, err := playlist.AddTrackToPlaylist(cmd.Context(), database, plIdentifier, trackIdent)
				if err != nil {
					return err
				}

				if agent.IsAgentMode() {
					cmd.Println("status: ok")
					cmd.Printf("playlist_id: %d\n", pl.ID)
					cmd.Printf("track_id: %d\n", tr.ID)
				} else {
					title := tr.Title.String
					if title == "" {
						title = tr.Filename.String
					}
					cmd.Printf("[OK] Added track %q [id:%d] to playlist %q [id:%d]\n", title, tr.ID, pl.Title, pl.ID)
				}
			}

			return nil
		},
	}
}

func newPlaylistTrackRmCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <pl|id> <tr|id|path...>",
		Short: "Remove track(s) from a playlist",
		Long: `Remove one or more tracks from a playlist.

This removes the track from the playlist only; the track remains in your master collection.

Arguments:
  <pl|id>           The name or numeric ID of the playlist.
  <tr|id|path...>   One or more track titles, IDs, or file paths to remove from the playlist.`,
		Example: `  engine-cli playlist track rm "Tech House" 42
  engine-cli playlist track rm "House" "Song Title"`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before playlist track rm: %w", err)
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

			plIdentifier := args[0]
			for _, trackIdent := range args[1:] {
				pl, tr, err := playlist.RemoveTrackFromPlaylist(cmd.Context(), database, plIdentifier, trackIdent)
				if err != nil {
					return err
				}

				if agent.IsAgentMode() {
					cmd.Println("status: ok")
					cmd.Printf("playlist_id: %d\n", pl.ID)
					cmd.Printf("removed_track_id: %d\n", tr.ID)
				} else {
					title := tr.Title.String
					if title == "" {
						title = tr.Filename.String
					}
					cmd.Printf("[OK] Removed track %q [id:%d] from playlist %q [id:%d]\n", title, tr.ID, pl.Title, pl.ID)
				}
			}

			return nil
		},
	}
}

func newPlaylistTrackMoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "move <src-pl|id> <dst-pl|id> <tr|id|path...>",
		Short: "Move track(s) from one playlist to another",
		Long: `Move track memberships from a source playlist to a destination playlist.

Arguments:
  <src-pl|id>       The name or numeric ID of the source playlist.
  <dst-pl|id>       The name or numeric ID of the destination playlist.
  <tr|id|path...>   One or more track titles, IDs, or file paths to transfer.`,
		Example: `  engine-cli playlist track move "House" "Tech House" 42
  engine-cli playlist track move "House" "Techno" "Song Title"`,
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			bkInfo, err := backup.AutoBackupIfNeeded(cmd.Context(), libDirFlag, noBackupFlag)
			if err != nil {
				return fmt.Errorf("automatic backup before playlist track move: %w", err)
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

			srcIdentifier := args[0]
			dstIdentifier := args[1]
			for _, trackIdent := range args[2:] {
				srcPL, dstPL, tr, err := playlist.MoveTrackBetweenPlaylists(cmd.Context(), database, srcIdentifier, dstIdentifier, trackIdent)
				if err != nil {
					return err
				}

				if agent.IsAgentMode() {
					cmd.Println("status: ok")
					cmd.Printf("src_playlist_id: %d\n", srcPL.ID)
					cmd.Printf("dst_playlist_id: %d\n", dstPL.ID)
					cmd.Printf("track_id: %d\n", tr.ID)
				} else {
					title := tr.Title.String
					if title == "" {
						title = tr.Filename.String
					}
					cmd.Printf("[OK] Moved track %q [id:%d] from %q to %q\n", title, tr.ID, srcPL.Title, dstPL.Title)
				}
			}

			return nil
		},
	}
}
