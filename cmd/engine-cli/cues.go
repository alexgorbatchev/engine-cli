package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/performance"
	"github.com/spf13/cobra"
)

func newTrackCueCommand() *cobra.Command {
	root := &cobra.Command{Use: "cue", Short: "Inspect and edit native saved hot cues A–H", Args: cobra.NoArgs}
	var inspectJSON bool
	inspect := &cobra.Command{
		Use: "inspect <tr|id|path>", Short: "Read saved cues, loops, and the revision required to edit a cue", Args: cobra.ExactArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
		RunE: func(command *cobra.Command, args []string) error {
			return runOperation(command, "track.cue.inspect", map[string]any{"identifier": args[0]}, inspectJSON, "")
		},
	}
	inspect.Flags().BoolVar(&inspectJSON, "json", false, "Output the saved performance as JSON")
	root.AddCommand(inspect, newCueEditCommand(false), newCueEditCommand(true))
	return root
}

func newCueEditCommand(clear bool) *cobra.Command {
	var slot, revision, name, color string
	var position float64
	var asJSON bool
	action, description := "set", "Set a hot cue's exact source position, name and color"
	if clear {
		action, description = "clear", "Clear one saved hot cue without changing other performance data"
	}
	command := &cobra.Command{
		Use: action + " <tr|id|path>", Short: description, Args: cobra.ExactArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
		RunE: func(command *cobra.Command, args []string) error {
			letter := strings.ToUpper(slot)
			if len(letter) != 1 || letter[0] < 'A' || letter[0] > 'H' {
				return errors.New("--slot must be one letter A through H")
			}
			values := map[string]any{"identifier": args[0], "slot": int(letter[0]-'A') + 1, "revision": revision}
			if !clear {
				values["position"], values["name"], values["color"] = position, name, color
			}
			return runOperation(command, "track.cue."+action, values, asJSON, "")
		},
	}
	command.Flags().StringVar(&slot, "slot", "", "Native hot-cue slot A through H")
	command.Flags().StringVar(&revision, "revision", "", "Exact saved-performance revision from cue inspect")
	command.Flags().BoolVar(&asJSON, "json", false, "Output the verified write result as JSON")
	_ = command.MarkFlagRequired("slot")
	_ = command.MarkFlagRequired("revision")
	if !clear {
		command.Flags().Float64Var(&position, "position", 0, "Exact source position in seconds, independent of playback speed")
		command.Flags().StringVar(&name, "name", "", "Cue name; an empty name is allowed")
		command.Flags().StringVar(&color, "color", "", "Cue color as #RRGGBB")
		_ = command.MarkFlagRequired("position")
		_ = command.MarkFlagRequired("color")
	}
	return command
}

func printCues(command *cobra.Command, result any) (bool, error) {
	output := command.OutOrStdout()
	switch value := result.(type) {
	case *performance.Saved:
		if _, err := fmt.Fprintf(output, "Track: %d\nRevision: %s\nHot cues: %d\nSaved loops: %d\n", value.TrackID, value.Revision, len(value.Cues), len(value.Loops)); err != nil {
			return true, err
		}
		for _, cue := range value.Cues {
			if _, err := fmt.Fprintf(output, "  %c: %.17g seconds — %s (%s)\n", 'A'+cue.Slot-1, cue.Position, cue.Name, cue.Color); err != nil {
				return true, err
			}
		}
		for _, loop := range value.Loops {
			if _, err := fmt.Fprintf(output, "  Loop %d: %.17g–%.17g seconds — %s (%s)\n", loop.Slot, loop.Start, loop.End, loop.Name, loop.Color); err != nil {
				return true, err
			}
		}
		return true, nil
	case *performance.Result:
		_, err := fmt.Fprintf(output, "Track: %d\nHot cue: %c\nChanged: %t\nRevision: %s\n", value.TrackID, 'A'+value.Slot-1, value.Changed, value.Revision)
		return true, err
	default:
		return false, nil
	}
}
