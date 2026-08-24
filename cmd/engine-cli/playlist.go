package main

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/playlist"
	"github.com/spf13/cobra"
)

func newPlaylistCommand() *cobra.Command {
	plCmd := &cobra.Command{
		Use:   "playlist",
		Short: "Inspect and list playlists and folders",
		Long: `Inspect and list playlists and folders in your Engine DJ collection.

  - Use 'engine-cli playlist list' to view the full folder and playlist tree.
  - Use 'engine-cli playlist inspect <playlist-id>' to view the songs inside a playlist.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	plCmd.AddCommand(newPlaylistListCommand())
	plCmd.AddCommand(newPlaylistInspectCommand())

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
		Use:   "inspect <playlist-id>",
		Short: "Display all songs inside a playlist",
		Long: `Display all songs and track lengths inside a specific playlist.

Arguments:
  <playlist-id>   The number ID of the playlist (e.g. 2). Run 'engine-cli playlist list' to see all playlist IDs.`,
		Example: `  engine-cli playlist inspect 2
  engine-cli -l "/Volumes/DJ_USB/Engine Library" playlist inspect 43`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid playlist ID %q: %w", args[0], err)
			}

			dbPath := filepath.Join(libDirFlag, "Database2", "m.db")
			database, err := db.Open(dbPath, true)
			if err != nil {
				return err
			}
			defer database.Close()

			details, err := playlist.InspectPlaylist(cmd.Context(), database, id)
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
