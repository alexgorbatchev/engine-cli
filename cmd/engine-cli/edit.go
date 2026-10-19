package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dj/engine-cli/internal/agent"
	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/details"
	"github.com/dj/engine-cli/internal/playlist"
	"github.com/dj/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func editDatabase(c *cobra.Command) (*sql.DB, error) {
	if _, err := backup.AutoBackupIfNeeded(c.Context(), libDirFlag, noBackupFlag); err != nil {
		return nil, fmt.Errorf("backing up before editing: %w", err)
	}
	d, err := db.Open(filepath.Join(libDirFlag, "Database2", "m.db"))
	if err != nil {
		return nil, err
	}
	return d, nil
}

func newTrackEditCommand() *cobra.Command {
	var edit track.MetadataEdit
	var asJSON bool
	text := map[string]*string{}
	var year, stars int64
	var artworkPath string
	c := &cobra.Command{Use: "update <tr|id|path>", Short: "Edit track information and star rating", Args: cobra.ExactArgs(1)}
	for _, name := range []string{"title", "artist", "album", "genre", "comment", "label", "composer", "remixer"} {
		text[name] = c.Flags().String(name, "", "Update the track's "+name)
	}
	c.Flags().Int64Var(&year, "year", 0, "Release year (0 clears the year)")
	c.Flags().Int64Var(&stars, "rating", 0, "Star rating from 0 (unrated) to 5")
	c.Flags().BoolVar(&asJSON, "json", false, "Print the updated track as JSON")
	c.Flags().StringVar(&artworkPath, "artwork", "", "Set cover artwork from a PNG, JPEG, or GIF image")
	c.Flags().BoolVar(&edit.ClearArtwork, "clear-artwork", false, "Remove the track's artwork association")
	c.RunE = func(c *cobra.Command, args []string) error {
		edit.Identifier = args[0]
		if c.Flags().Changed("artwork") {
			edit.ArtworkPath = &artworkPath
		}
		for _, field := range []struct {
			name string
			dest **string
		}{{"title", &edit.Title}, {"artist", &edit.Artist}, {"album", &edit.Album}, {"genre", &edit.Genre}, {"comment", &edit.Comment}, {"label", &edit.Label}, {"composer", &edit.Composer}, {"remixer", &edit.Remixer}} {
			if c.Flags().Changed(field.name) {
				*field.dest = text[field.name]
			}
		}
		if c.Flags().Changed("year") {
			edit.Year = &year
		}
		if c.Flags().Changed("rating") {
			edit.RatingStars = &stars
		}
		if err := edit.Validate(); err != nil {
			return err
		}
		d, err := editDatabase(c)
		if err != nil {
			return err
		}
		defer d.Close()
		t, err := track.EditMetadata(c.Context(), d, edit)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(c.OutOrStdout()).Encode(details.Track(t))
		}
		if agent.IsAgentMode() {
			_, err = fmt.Fprintf(c.OutOrStdout(), "status: ok\ntitle: %s\nrating_stars: %d\n", t.Title.String, t.Rating.Int64/track.RatingUnitsPerStar)
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Updated track %q.\n", t.Title.String)
		return err
	}
	return c
}

func newPlaylistRenameCommand() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "rename <pl|id> <name>", Short: "Rename a playlist or folder", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		d, err := editDatabase(c)
		if err != nil {
			return err
		}
		defer d.Close()
		p, err := playlist.RenamePlaylist(c.Context(), d, args[0], args[1])
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(c.OutOrStdout()).Encode(p)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Renamed playlist to %q.\n", p.Title)
		return err
	}}
	c.Flags().BoolVar(&asJSON, "json", false, "Print the renamed playlist as JSON")
	return c
}
