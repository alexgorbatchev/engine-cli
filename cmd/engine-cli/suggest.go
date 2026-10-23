package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/llm"
	"github.com/alexgorbatchev/engine-cli/internal/suggest"
	"github.com/spf13/cobra"
)

func addLLMFlags(c *cobra.Command, config *llm.Config) {
	c.Flags().StringVar(&config.Provider, "llm-provider", config.Provider, "AI provider: openai, anthropic, openrouter, or openai-compat (ENGINE_LLM_PROVIDER)")
	c.Flags().StringVar(&config.Model, "llm-model", config.Model, "Model to use for suggestions (ENGINE_LLM_MODEL; required for nonempty selections)")
	c.Flags().StringVar(&config.BaseURL, "llm-base-url", config.BaseURL, "Custom AI service URL (ENGINE_LLM_BASE_URL)")
}

func newSuggestionCommand() *cobra.Command {
	config := llm.FromEnv()
	var playlists []string
	var asJSON bool
	c := &cobra.Command{Use: "suggest [id|ref...]", Short: "Suggest title and artist fixes for review", Args: cobra.MaximumNArgs(details.MaxItems),
		Long: "Review AI suggestions for title and artist cleanup. Choose track IDs/references or --playlists. Selected title, artist, album, and filename are sent to your configured AI service. Your library and audio files remain unchanged.",
		RunE: func(c *cobra.Command, args []string) error {
			selection := suggest.Selection{Tracks: args, Playlists: playlists}
			r, err := suggest.Run(c.Context(), libDirFlag, selection, config)
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
	addLLMFlags(c, &config)
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
