package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/duplicate"
	"github.com/dj/engine-cli/internal/identity"
	"github.com/spf13/cobra"
)

func newDuplicateCommand() *cobra.Command {
	group := &cobra.Command{Use: "duplicate", Short: "Find and compare possible duplicate tracks", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	var mode string
	var jsonOutput bool
	list := &cobra.Command{Use: "list", Short: "List possible duplicates without changing the library", Args: cobra.NoArgs,
		Long: "Find candidates by artist/title or filename/duration. Matching metadata does not prove identical audio. Overlapping matches appear together once.",
		RunE: func(c *cobra.Command, _ []string) error {
			return readDuplicates(func(d *sql.DB) error {
				if mode == "audio" {
					r, err := duplicate.AudioGroups(c.Context(), d, libDirFlag, duplicate.AudioOptions{})
					if err != nil {
						return err
					}
					return printAudioReport(c, r, jsonOutput)
				}
				groups, err := duplicate.List(c.Context(), d, mode)
				if err != nil {
					return err
				}
				if jsonOutput {
					return json.NewEncoder(c.OutOrStdout()).Encode(groups)
				}
				if _, err = fmt.Fprintf(c.OutOrStdout(), "groups: %d\n", len(groups)); err != nil {
					return err
				}
				for i, g := range groups {
					if _, err = fmt.Fprintf(c.OutOrStdout(), "group: %d\n", i+1); err != nil {
						return err
					}
					for _, t := range g.Tracks {
						if err = printDuplicate(c, t); err != nil {
							return err
						}
					}
				}
				return nil
			})
		}}
	list.Flags().StringVar(&mode, "type", "all", "Match by all, metadata, filename-len, or indexed audio")
	list.Flags().BoolVar(&jsonOutput, "json", false, "Return structured results as JSON")
	inspect := &cobra.Command{Use: "inspect <id...>", Short: "Compare track quality, saved cues, and playlists", Args: cobra.MinimumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return readDuplicates(func(d *sql.DB) error {
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if strings.HasPrefix(arg, "eng:") {
					id, err = identity.Resolve(c.Context(), d, libDirFlag, "track", arg)
				}
				if err != nil || id <= 0 {
					return fmt.Errorf("%q is not a positive track ID; use track duplicate list to find IDs", arg)
				}
				tr, err := duplicate.Inspect(c.Context(), d, id)
				if err != nil {
					return err
				}
				if err = printDuplicate(c, *tr); err != nil {
					return err
				}
			}
			return nil
		})
	}}
	group.AddCommand(list, inspect, newDuplicateMergeCommand(), newDuplicateScanCommand())
	return group
}

func readDuplicates(run func(*sql.DB) error) error {
	d, err := db.Open(filepath.Join(libDirFlag, "Database2", "m.db"), true)
	if err != nil {
		return err
	}
	defer d.Close()
	return run(d)
}

func printDuplicate(c *cobra.Command, t duplicate.Track) error {
	format := "Track %d: %q — %q\n  Format: %s; bitrate: %d kbps; duration: %d; bytes: %d\n  Path: %s\n  Cues: %t; loops: %t; played: %t; last played: %d\n"
	if agent.IsAgentMode() {
		format = "id: %d title: %q artist: %q\nformat: %s bitrate: %d length: %d bytes: %d\npath: %s\ncues: %t loops: %t played: %t last_played: %d\n"
	}
	if _, err := fmt.Fprintf(c.OutOrStdout(), format, t.ID, t.Title, t.Artist, t.FileType, t.Bitrate, t.Length, t.FileBytes, displayPath(t.Path), t.HasCues, t.HasLoops, t.IsPlayed, t.TimeLastPlayed); err != nil {
		return err
	}
	for _, name := range t.Playlists {
		if _, err := fmt.Fprintf(c.OutOrStdout(), "playlist: %q\n", name); err != nil {
			return err
		}
	}
	return nil
}
