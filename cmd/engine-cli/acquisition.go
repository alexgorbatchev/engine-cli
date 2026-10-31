package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/state"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Injectable process boundary keeps command tests independent of installed tools.
var acquisitionRunner enginesync.CommandRunner

type acquisitionFlag struct{ name, kind, description string }

var trackFetchFlags = []acquisitionFlag{
	{"sources", "string", "Comma-separated sources: youtube, soundcloud, bandcamp"},
	{"skip-verify", "bool", "Skip audio quality verification"}, {"skip-metadata", "bool", "Skip online metadata lookup"},
	{"no-cache", "bool", "Bypass cached results"}, {"auto-install", "bool", "Install missing upstream dependencies"},
	{"min-bandwidth-khz", "float", "Minimum accepted spectral bandwidth in kHz"},
	{"confirm-fingerprint", "bool", "Require fingerprint confirmation (explicit true or false)"},
	{"js-runtime", "string", "JavaScript runtime used by the downloader"},
}
var mixFetchFlags = []acquisitionFlag{
	{"extractor", "string", "Tracklist extractor: auto or youtube-comments"},
	{"llm-provider", "string", "AI provider used to extract tracklists"}, {"llm-model", "string", "AI model used to extract tracklists"},
	{"sources", "string", "Comma-separated audio sources"}, {"skip-verify", "bool", "Skip audio quality verification"},
	{"skip-metadata", "bool", "Skip online metadata lookup"}, {"no-cache", "bool", "Bypass cached results"},
	{"auto-install", "bool", "Install missing upstream dependencies"},
}
var continuationFlags = []acquisitionFlag{{"selection-token", "string", "Persisted candidate selection token"}, {"candidate-url", "string", "URL from the selected candidate set"}}
var importFlags = []acquisitionFlag{{"import", "bool", "Import successful downloads into the collection"}, {"to-playlist", "string", "Assign imported tracks to this playlist"}}
var tagFlags = []acquisitionFlag{
	{"artist", "string", "Set artist; an explicit empty value clears it"}, {"title", "string", "Set title; an explicit empty value clears it"},
	{"album", "string", "Set album; an explicit empty value clears it"}, {"date", "string", "Set release date; an explicit empty value clears it"},
	{"genre", "string", "Set genre; an explicit empty value clears it"}, {"comment", "string", "Set comment; an explicit empty value clears it"},
	{"artwork", "string", "Artwork file path or URL"}, {"source-url", "string", "Metadata lookup source URL"},
	{"refresh", "bool", "Refresh metadata using online sources"}, {"dry-run", "bool", "Preview the tag update"},
	{"no-cache", "bool", "Bypass cached metadata"}, {"auto-install", "bool", "Install missing upstream dependencies"},
}

func addAcquisitionCommands(root *cobra.Command) {
	track, _, _ := root.Find([]string{"track"})
	for _, action := range []string{"fetch", "candidates"} {
		flags := append([]acquisitionFlag{}, trackFetchFlags...)
		if action == "fetch" {
			flags = append(flags, continuationFlags...)
			flags = append(flags, importFlags...)
		}
		track.AddCommand(acquisitionCommand("track."+action, action+" <query>", "Find and acquire individual audio tracks", "query", flags))
	}
	track.AddCommand(acquisitionCommand("track.verify", "verify [url|path|identifier]", "Verify the spectral quality of remote, local, collection, or staged audio", "identifier", []acquisitionFlag{{"target", "string", "Verify this URL or local path"}, {"staging-id", "string", "Verify this staged item instead of a collection track"}, {"min-bandwidth-khz", "float", "Minimum accepted bandwidth in kHz"}, {"auto-install", "bool", "Install missing verification dependencies"}}))
	tags := &cobra.Command{Use: "tags", Short: "Inspect and update tags embedded in audio files", Args: cobra.NoArgs}
	targetFlags := []acquisitionFlag{{"staging-id", "string", "Select a staged item"}, {"staging-ids", "strings", "Select staged items; repeat or separate with commas"}, {"path", "string", "Select a local audio file"}, {"paths", "strings", "Select local audio paths; repeat or separate with commas"}}
	tags.AddCommand(acquisitionCommand("track.tags.inspect", "inspect [path|identifier...]", "Inspect actual audio file tags", "identifiers", targetFlags))
	tags.AddCommand(acquisitionCommand("track.tags.update", "update [path|identifier...]", "Update embedded tags while preserving original audio and collection metadata", "identifiers", append(append([]acquisitionFlag{}, targetFlags...), tagFlags...)))
	track.AddCommand(tags)
	artwork, _, _ := track.Find([]string{"artwork"})
	artwork.AddCommand(acquisitionCommand("track.artwork.embed", "embed [path|identifier...]", "Embed artwork without re-encoding audio", "identifiers", append(append([]acquisitionFlag{}, targetFlags...), acquisitionFlag{"artwork", "string", "Artwork file path or URL"})))
	mix := &cobra.Command{Use: "mix", Short: "Extract tracklists and acquire ordered mixes", Args: cobra.NoArgs}
	for _, action := range []string{"inspect", "candidates", "fetch"} {
		flags := append([]acquisitionFlag{}, mixFetchFlags...)
		if action == "fetch" {
			flags = append(flags, continuationFlags...)
			flags = append(flags, importFlags...)
		}
		mix.AddCommand(acquisitionCommand("mix."+action, action+" <query>", "Inspect or acquire a mix using native extraction", "query", flags))
	}
	root.AddCommand(mix)
	ai := &cobra.Command{Use: "ai", Short: "Inspect tracklist AI providers", Args: cobra.NoArgs}
	ai.AddCommand(acquisitionCommand("ai.list", "list", "List available AI providers", "", nil), acquisitionCommand("ai.inspect", "inspect <provider>", "Inspect an AI provider", "provider", nil))
	root.AddCommand(ai)
	tool := &cobra.Command{Use: "tool", Short: "Manage tools and their dependencies", Args: cobra.NoArgs}
	for _, action := range []string{"inspect", "upgrade"} {
		tool.AddCommand(acquisitionCommand("tool."+action, action+" <tool>", "Inspect or upgrade a managed tool", "tool", nil))
	}
	deps := &cobra.Command{Use: "dependencies", Short: "Inspect, install, or update upstream dependencies", Args: cobra.NoArgs}
	for _, action := range []string{"inspect", "install", "update"} {
		var flags []acquisitionFlag
		if action != "inspect" {
			flags = []acquisitionFlag{{"dependencies", "strings", "Dependency names; repeat or use commas (default: all)"}}
		}
		deps.AddCommand(acquisitionCommand("tool.dependencies."+action, action+" <tool>", "Manage native upstream dependencies", "tool", flags))
	}
	tool.AddCommand(deps)
	root.AddCommand(tool)
	staged, _, _ := root.Find([]string{"staging"})
	staged.AddCommand(acquisitionCommand("staging.retry", "retry <id>", "Retry a staged acquisition or its failed mix tracks", "id", nil))
}

func acquisitionCommand(operation, use, short, positional string, flags []acquisitionFlag) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, Annotations: map[string]string{"engine-api-operation": "true"}}
	var statePath string
	if strings.HasPrefix(operation, "library.") {
		command.Flags().StringVar(&statePath, "state-db", "", "Library preferences database (default: XDG state directory)")
	}
	if positional != "" {
		command.Args = cobra.ExactArgs(1)
		if positional == "identifier" {
			command.Args = cobra.MaximumNArgs(1)
		}
		if positional == "identifiers" {
			command.Args = cobra.ArbitraryArgs
		}
	}
	for _, flag := range flags {
		switch flag.kind {
		case "bool":
			command.Flags().Bool(flag.name, false, flag.description)
		case "float":
			command.Flags().Float64(flag.name, 0, flag.description)
		case "strings":
			command.Flags().StringSlice(flag.name, nil, flag.description)
		default:
			command.Flags().String(flag.name, "", flag.description)
		}
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	command.RunE = func(c *cobra.Command, args []string) error {
		values := map[string]any{}
		if positional != "" && len(args) > 0 {
			key := positional
			if positional == "identifiers" {
				key = "identifiers"
				for _, arg := range args {
					if !collectionArgument(arg) {
						key = "paths"
						break
					}
				}
				if len(args) == 1 {
					if key == "paths" {
						key = "path"
					} else {
						key = "identifier"
					}
					values[key] = args[0]
				} else {
					values[key] = args
				}
			} else {
				if operation == "track.verify" && !collectionArgument(args[0]) {
					key = "target"
				}
				values[key] = args[0]
			}
		}
		var flagErr error
		c.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
			if !flag.Changed || flag.Name == "json" || flag.Name == "state-db" {
				return
			}
			key := strings.ReplaceAll(flag.Name, "-", "_")
			var value any
			var err error
			switch flag.Value.Type() {
			case "bool":
				value, err = c.Flags().GetBool(flag.Name)
			case "float64":
				value, err = c.Flags().GetFloat64(flag.Name)
			case "stringSlice":
				value, err = c.Flags().GetStringSlice(flag.Name)
			default:
				value, err = c.Flags().GetString(flag.Name)
			}
			if err != nil {
				flagErr = err
			}
			values[key] = value
		})
		if flagErr != nil {
			return flagErr
		}
		return runOperation(c, operation, values, asJSON, statePath)
	}
	return command
}

// runOperation runs an API operation in process with the same validation, guards, snapshots, and undo journal as
// HTTP jobs, then prints its result.
func runOperation(c *cobra.Command, operation string, values map[string]any, asJSON bool, statePath string) error {
	parameters, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if strings.HasPrefix(operation, "library.") && statePath == "" {
		statePath, err = state.DefaultPath()
		if err != nil {
			return err
		}
	}
	server, err := api.New(api.Config{StatePath: statePath, LibraryDir: libDirFlag, NoBackup: noBackupFlag, CheckSystem: querySystem, ToolRunner: acquisitionRunner})
	if err != nil {
		return err
	}
	defer server.Close()
	result, historyID, runErr := server.RunOperation(c.Context(), operation, parameters)
	if batch, ok := result.(*api.MediaBatchResult); ok && runErr == nil {
		// A partial batch succeeds as an API job, but the command still exits unsuccessfully.
		runErr = batch.Err()
	}
	if historyID != "" {
		historyOutput := c.ErrOrStderr()
		if operation == "track.upgrade" && !asJSON {
			historyOutput = c.OutOrStdout()
		}
		if _, err := fmt.Fprintf(historyOutput, "operation_id: %s\n", historyID); err != nil {
			return err
		}
	}
	if result != nil {
		if handled, err := printPlaylistReorder(c, result, asJSON); handled {
			return errors.Join(runErr, err)
		}
		if handled, err := printMetadataTransform(c, result, asJSON); handled {
			return errors.Join(runErr, err)
		}
		if handled, err := printRekordbox(c.OutOrStdout(), result, asJSON); handled {
			return errors.Join(runErr, err)
		}
		if !asJSON {
			if handled, err := printCues(c, result); handled {
				return errors.Join(runErr, err)
			}
			if handled, err := printCustomTags(c, result); handled {
				return errors.Join(runErr, err)
			}
		}
		if handled, err := printPlaylistExport(c, result, asJSON); handled {
			return err
		}
		if handled, err := printSession(c.OutOrStdout(), result, asJSON); handled {
			return err
		}
		if plan, ok := result.(*playlist.ImportPlan); ok {
			display := *plan
			display.Path = displayPath(display.Path)
			display.Entries = append([]playlist.ImportEntry{}, plan.Entries...)
			for i := range display.Entries {
				display.Entries[i].Path = displayPath(display.Entries[i].Path)
			}
			if !asJSON {
				return printPlaylistImportPlan(c, &display)
			}
			result = &display
		}
		if imported, ok := result.(*playlist.ImportResult); ok && !asJSON {
			_, err := fmt.Fprintf(c.OutOrStdout(), "Created playlist %d: %d tracks, %d repeated entries omitted, %d unresolved entries skipped.\n", imported.PlaylistID, imported.ImportedCount, imported.DuplicateCount, imported.SkippedCount)
			return err
		}
		if created, ok := result.(*api.CreatedLibrary); ok {
			display := *created
			display.Path = displayPath(display.Path)
			result = &display
		}
		if plan, ok := result.(*libraryops.Plan); ok {
			result = displayLibraryPlan(plan)
		}
		if libraries, ok := result.([]api.KnownLibrary); ok {
			display := append([]api.KnownLibrary{}, libraries...)
			for i := range display {
				display[i].Path = displayPath(display[i].Path)
			}
			result = display
		}
		if operation == "track.upgrade" && !asJSON && runErr == nil {
			return printTrackUpgrade(c, result.(api.UpgradedTrack))
		}
		encoder := json.NewEncoder(c.OutOrStdout())
		if !asJSON {
			encoder.SetIndent("", "  ")
		}
		if err := encoder.Encode(result); err != nil {
			return err
		}
	}
	return runErr
}

func collectionArgument(value string) bool {
	if strings.HasPrefix(value, "eng:") {
		return true
	}
	_, err := strconv.ParseInt(value, 10, 64)
	return err == nil
}
