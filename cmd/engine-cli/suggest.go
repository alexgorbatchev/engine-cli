package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/suggest"
	"github.com/spf13/cobra"
)

func addAskFlags(c *cobra.Command, options *acquire.AskOptions) {
	c.Flags().StringVar(&options.Harness, "harness", "", "Authenticated harness: claude or codex")
	c.Flags().StringVar(&options.Model, "model", "", "Harness model (default: harness configuration)")
	c.Flags().StringVar(&options.Effort, "effort", "", "Native harness reasoning effort")
	c.Flags().StringVar(&options.Thinking, "thinking", "", "Native harness thinking: auto, on, or off")
	c.Flags().StringVar(&options.HarnessConfigMode, "harness-config-mode", "", "Harness configuration: inherit (default) or clean")
	c.Flags().StringVar(&options.HarnessConfigFile, "harness-config-file", "", "Native harness configuration file")
	c.Flags().StringVar(&options.HarnessHomeDir, "harness-home-dir", "", "Alternate harness configuration and authentication directory")
	c.Flags().StringVar(&options.Timeout, "timeout", "", "Timeout per batch, such as 5m (default: agent-ask-cli's 5m)")
	c.Flags().BoolVar(&options.AutoInstall, "auto-install", false, "Install or update agent-ask-cli when needed")
}

func newSuggestionCommand() *cobra.Command {
	var options acquire.AskOptions
	var playlists []string
	var asJSON bool
	c := &cobra.Command{Use: "suggest [id|ref...]", Short: "Suggest title and artist fixes for review", Args: cobra.MaximumNArgs(details.MaxItems),
		Long: "Review AI suggestions for title and artist cleanup. Choose track IDs/references or --playlists and --harness claude|codex. Selected title, artist, album, and filename are sent through agent-ask-cli using the harness's existing authentication. Your library and audio files remain unchanged.",
		RunE: func(c *cobra.Command, args []string) error {
			selection := suggest.Selection{Tracks: args, Playlists: playlists}
			r, err := suggest.Run(c.Context(), libDirFlag, selection, options, acquire.Client{})
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(c.OutOrStdout()).Encode(r)
			}
			return printSuggestions(c.OutOrStdout(), r)
		}}
	c.Flags().StringSliceVar(&playlists, "playlists", nil, "Suggest fixes for direct members of these playlist IDs/references (comma-separated or repeated)")
	c.Flags().BoolVar(&asJSON, "json", false, "Print original values and suggestions as JSON")
	addAskFlags(c, &options)
	return c
}

func printSuggestions(out io.Writer, report *suggest.Report) error {
	for _, item := range report.Items {
		title, artist := suggestionText(item.Current.Title), suggestionText(item.Current.Artist)
		if item.Suggestion == nil {
			if _, err := fmt.Fprintf(out, "track: %d ref: %q status: unchanged title: %q artist: %q\n", item.ID, item.Ref, title, artist); err != nil {
				return err
			}
			continue
		}
		p := item.Suggestion
		var err error
		if agent.IsAgentMode() {
			_, err = fmt.Fprintf(out, "track: %d ref: %q title_before: %q title_after: %q artist_before: %q artist_after: %q reason: %q\n",
				item.ID, item.Ref, title, suggestionText(p.Title), artist, suggestionText(p.Artist), p.Reason)
		} else {
			_, err = fmt.Fprintf(out, "Track %d\n  Title: %q -> %q\n  Artist: %q -> %q\n  Reason: %q\n",
				item.ID, title, suggestionText(p.Title), artist, suggestionText(p.Artist), p.Reason)
		}
		if err != nil {
			return err
		}
	}
	if len(report.Items) == 0 {
		_, err := fmt.Fprintln(out, "No tracks selected by these playlists.")
		return err
	}
	return nil
}

func suggestionText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
