package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/beatgrid"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func newTrackAnalyzeCommand() *cobra.Command {
	var s track.AnalysisSelection
	var asJSON bool
	c := &cobra.Command{
		Use:   "analyze <tr|id|path...>",
		Short: "Analyze BPM, musical key, and beat grid from the audio",
		Long: `Analyze tracks' audio and write their BPM, musical key, and beat grid.

Without --bpm, --key, or --grid every analysis runs. --missing-only keeps values tracks already have, and locked
beat grids are never replaced. Each track is written as soon as its analysis finishes, after rechecking that Engine DJ
is closed; one undo record covers every written track.`,
		Example: `  engine-cli track analyze 12 13 14
  engine-cli track analyze --key --missing-only 12
  engine-cli track analyze --grid --bpm-range 68-135 12`,
		Args:        cobra.RangeArgs(1, track.MaxMetadataBatch),
		Annotations: map[string]string{"engine-api-operation": "true"},
	}
	c.Flags().BoolVar(&s.BPM, "bpm", false, "Analyze BPM")
	c.Flags().BoolVar(&s.Key, "key", false, "Analyze the musical key")
	c.Flags().BoolVar(&s.Grid, "grid", false, "Analyze the beat grid")
	c.Flags().BoolVar(&s.MissingOnly, "missing-only", false, "Keep BPM, keys, and beat grids tracks already have")
	c.Flags().StringVar(&s.BPMRange, "bpm-range", track.DefaultBPMRange, "Tempo range: 58-115, 68-135, 78-155, 88-175, or 98-195")
	c.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	c.RunE = func(c *cobra.Command, args []string) error {
		if !s.BPM && !s.Key && !s.Grid {
			s.BPM, s.Key, s.Grid = true, true, true
		}
		values, err := operationValues(track.AnalysisRequest{Identifiers: args, AnalysisSelection: s})
		if err != nil {
			return err
		}
		return runOperation(c, "track.analyze", values, asJSON, "")
	}
	return c
}

func newTrackGridCommand() *cobra.Command {
	grid := &cobra.Command{Use: "grid", Short: "Inspect and edit beat grids", Args: cobra.NoArgs}
	grid.AddCommand(newTrackGridUpdateCommand())
	return grid
}

func newTrackGridUpdateCommand() *cobra.Command {
	var e beatgrid.Edit
	var dryRun, double, half, reset, asJSON bool
	c := &cobra.Command{
		Use:   "update <tr|id|path>",
		Short: "Apply one beat-grid edit and save it with undo",
		Long: `Apply one edit to a track's saved beat grid and save it, updating the track's BPM to match.

Choose exactly one edit. Times are seconds on the track's timeline; markers count from 0 at the start of the grid.
--dry-run prints the edited grid without saving it. Saving is Desktop-protected and undoable.`,
		Example: `  engine-cli track grid update 12 --shift -0.012
  engine-cli track grid update 12 --tempo 124.5
  engine-cli track grid update 12 --marker-add 95.5 --dry-run
  engine-cli track grid update 12 --tap 3.02,3.5,3.98,4.46`,
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
	}
	var shift, downbeat, tempo, markerAdd, markerMove float64
	var barShift int64
	var marker int
	var markerRemove int
	var taps []float64
	c.Flags().Float64Var(&shift, "shift", 0, "Move every beat by this many seconds")
	c.Flags().Int64Var(&barShift, "bar-shift", 0, "Move every downbeat by 1 to 3 beats (negative moves earlier)")
	c.Flags().Float64Var(&downbeat, "downbeat", 0, "Make the beat nearest this time start a bar")
	c.Flags().BoolVar(&double, "double", false, "Double the tempo")
	c.Flags().BoolVar(&half, "half", false, "Halve the tempo")
	c.Flags().Float64Var(&tempo, "tempo", 0, "Set the BPM of the segment chosen by --marker")
	c.Flags().IntVar(&marker, "marker", 0, "Tempo segment for --tempo, or the marker for --marker-move")
	c.Flags().Float64Var(&markerAdd, "marker-add", 0, "Add a tempo marker on the beat nearest this time")
	c.Flags().Float64Var(&markerMove, "marker-move", 0, "Move the marker chosen by --marker to this time")
	c.Flags().IntVar(&markerRemove, "marker-remove", 0, "Remove this tempo marker")
	c.Flags().Float64SliceVar(&taps, "tap", nil, "Fit the tempo and beat phase to these tap times")
	c.Flags().BoolVar(&reset, "reset", false, "Restore the grid from the last analysis")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Print the edited grid without saving it")
	c.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	c.RunE = func(c *cobra.Command, args []string) error {
		chosen := 0
		for _, choice := range []struct {
			flag string
			edit beatgrid.Edit
		}{
			{"shift", beatgrid.Edit{Kind: beatgrid.EditShift, Seconds: shift}},
			{"bar-shift", beatgrid.Edit{Kind: beatgrid.EditBarShift, Beats: barShift}},
			{"downbeat", beatgrid.Edit{Kind: beatgrid.EditDownbeat, Seconds: downbeat}},
			{"double", beatgrid.Edit{Kind: beatgrid.EditScale, Factor: 2}},
			{"half", beatgrid.Edit{Kind: beatgrid.EditScale, Factor: 0.5}},
			{"tempo", beatgrid.Edit{Kind: beatgrid.EditTempo, BPM: tempo, Marker: marker}},
			{"marker-add", beatgrid.Edit{Kind: beatgrid.EditMarkerAdd, Seconds: markerAdd}},
			{"marker-move", beatgrid.Edit{Kind: beatgrid.EditMarkerMove, Seconds: markerMove, Marker: marker}},
			{"marker-remove", beatgrid.Edit{Kind: beatgrid.EditMarkerRemove, Marker: markerRemove}},
			{"tap", beatgrid.Edit{Kind: beatgrid.EditTap, Taps: taps}},
			{"reset", beatgrid.Edit{Kind: beatgrid.EditReset}},
		} {
			if c.Flags().Changed(choice.flag) {
				e = choice.edit
				chosen++
			}
		}
		if chosen != 1 {
			return fmt.Errorf("choose exactly one grid edit")
		}
		d, err := db.Open(filepath.Join(libDirFlag, "Database2", "m.db"), true)
		if err != nil {
			return err
		}
		defer d.Close()
		t, err := db.FindTrack(c.Context(), d, args[0])
		if err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("track %q was not found", args[0])
		}
		data, err := beatgrid.Load(c.Context(), d, t.ID)
		if err != nil {
			return err
		}
		if data == nil {
			return fmt.Errorf("analyze the track's beat grid before editing it: engine-cli track analyze --grid %s", args[0])
		}
		edited, err := beatgrid.Apply(beatgrid.SourceOf(*data), nil, e)
		if err != nil {
			return err
		}
		if dryRun {
			encoder := json.NewEncoder(c.OutOrStdout())
			if !asJSON {
				encoder.SetIndent("", "  ")
			}
			return encoder.Encode(map[string]any{"grid": edited})
		}
		return runOperation(c, "track.grid.update", map[string]any{"identifier": fmt.Sprint(t.ID), "anchors": edited.Anchors}, asJSON, "")
	}
	return c
}

// operationValues converts typed operation parameters into the JSON object an operation decodes.
func operationValues(parameters any) (map[string]any, error) {
	raw, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	return values, json.Unmarshal(raw, &values)
}

// printAnalysisReport prints one line per analysis of each track.
func printAnalysisReport(c *cobra.Command, report *track.AnalysisReport) error {
	for _, t := range report.Tracks {
		for _, outcome := range []struct {
			name    string
			outcome *track.AnalysisOutcome
		}{{"bpm", t.BPM}, {"key", t.Key}, {"grid", t.Grid}} {
			o := outcome.outcome
			if o == nil {
				continue
			}
			line := fmt.Sprintf("track %d %s: %s", t.TrackID, outcome.name, o.Status)
			switch {
			case o.Error != nil:
				line += " (" + o.Error.Message + ")"
			case o.Key != nil:
				line += fmt.Sprintf(" %d", *o.Key)
			case o.BPM > 0:
				line += fmt.Sprintf(" %.2f BPM", o.BPM)
			}
			if _, err := fmt.Fprintln(c.OutOrStdout(), line); err != nil {
				return err
			}
		}
	}
	return nil
}
