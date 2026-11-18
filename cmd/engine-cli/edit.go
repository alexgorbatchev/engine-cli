package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/nateranda/djtools/musicalkey"
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

func newTrackEditCommand(batch bool) *cobra.Command {
	var edit track.MetadataEdit
	var asJSON bool
	text := map[string]*string{}
	var year, stars int64
	var key string
	var artworkPath string
	var dryRun bool
	var planToken string
	c := &cobra.Command{Use: "update <tr|id|path>", Short: "Edit track information and star rating", Args: cobra.ExactArgs(1)}
	if batch {
		c.Use = "update <tr|id|path...>"
		c.Short = "Edit selected tracks together with one undo record"
		c.Args = cobra.RangeArgs(1, track.MaxMetadataBatch)
		// The shared API operation reviews, protects and records the edit exactly as HTTP does.
		c.Annotations = map[string]string{"engine-api-operation": "true"}
		c.Flags().BoolVar(&dryRun, "dry-run", false, "Review each track's changes without changing the collection")
		c.Flags().StringVar(&planToken, "plan-token", "", "Apply only the exact reviewed plan; without it the edit applies at once")
	}
	for _, name := range []string{"title", "artist", "album", "genre", "comment", "label", "composer", "remixer"} {
		text[name] = c.Flags().String(name, "", "Update the track's "+name)
	}
	c.Flags().Int64Var(&year, "year", 0, "Release year (0 clears the year)")
	c.Flags().Int64Var(&stars, "rating", 0, "Star rating from 0 (unrated) to 5")
	c.Flags().StringVar(&key, "key", "", "Musical key in Camelot (8A), Open Key (1m), or by name (Am, F#m, Bb); none clears it")
	jsonHelp := "Print the edited track IDs as JSON"
	if batch {
		jsonHelp = "Print the review or the per-track changes as JSON"
	}
	c.Flags().BoolVar(&asJSON, "json", false, jsonHelp)
	if !batch {
		c.Flags().StringVar(&artworkPath, "artwork", "", "Set cover artwork from a PNG, JPEG, or GIF image")
		c.Flags().BoolVar(&edit.ClearArtwork, "clear-artwork", false, "Remove the track's artwork association")
	}
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
		if c.Flags().Changed("key") {
			index, err := musicalkey.Parse(key)
			if err != nil {
				return err
			}
			edit.Key = &index
		}
		if err := edit.Validate(); err != nil {
			return err
		}
		if batch {
			return runBatchMetadataEdit(c, track.BatchMetadataEdit{Identifiers: args, MetadataFields: edit.MetadataFields, DryRun: dryRun, PlanToken: planToken}, asJSON)
		}
		d, err := editDatabase(c)
		if err != nil {
			return err
		}
		defer d.Close()
		id, err := track.EditMetadata(c.Context(), d, edit)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]int64{"track_id": id})
		}
		if agent.IsAgentMode() {
			_, err = fmt.Fprintf(c.OutOrStdout(), "status: ok\ntrack_id: %d\n", id)
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Updated track [id:%d].\n", id)
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
