package main

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/spf13/cobra"
)

func newPlaylistReorderCommand() *cobra.Command {
	var opts playlist.ReorderOptions
	var asJSON bool
	command := &cobra.Command{
		Use:         "reorder <id|ref>",
		Short:       "Review and change the saved track order within a playlist",
		Long:        "Move selected displayed rows to a gap in the saved playlist order. Rows and gaps are zero-based; the gap after the final row equals the displayed row count. Review with --dry-run, then repeat the same selection and view with --plan-token. Filtered views preserve hidden memberships; --descending describes a reversed saved-order view.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
		RunE: func(c *cobra.Command, args []string) error {
			values := map[string]any{
				"playlist": args[0], "view": opts.View, "rows": opts.Rows, "drop": opts.Drop,
				"descending": opts.Descending, "dry_run": opts.DryRun, "plan_token": opts.PlanToken,
			}
			return runOperation(c, "playlist.reorder", values, asJSON, "")
		},
	}
	f := command.Flags()
	f.Int64SliceVar(&opts.View, "view", nil, "Displayed library-local track IDs in saved order; omit for the full playlist")
	f.IntSliceVar(&opts.Rows, "rows", nil, "Selected zero-based displayed row indexes; repeat or separate with commas")
	f.IntVar(&opts.Drop, "drop", 0, "Zero-based insertion gap before a row, or the gap after the last row")
	f.BoolVar(&opts.Descending, "descending", false, "The displayed view reverses the saved membership order")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Review the resulting order without writing")
	f.StringVar(&opts.PlanToken, "plan-token", "", "Apply the exact reviewed plan; required without --dry-run")
	f.BoolVar(&asJSON, "json", false, "Print compact structured results")
	_ = command.MarkFlagRequired("rows")
	_ = command.MarkFlagRequired("drop")
	return command
}

func printPlaylistReorder(command *cobra.Command, result any, asJSON bool) (bool, error) {
	if asJSON {
		return false, nil
	}
	output := command.OutOrStdout()
	switch value := result.(type) {
	case *playlist.ReorderPlan:
		if agent.IsAgentMode() {
			_, err := fmt.Fprintf(output, "playlist_id:%d track_ids:%v can_apply:%t plan_token:%s\n", value.PlaylistID, value.TrackIDs, value.CanApply, value.PlanToken)
			return true, err
		}
		_, err := fmt.Fprintf(output, "Playlist: %d\nResulting track order: %v\nCan apply: %t\nPlan token: %s\nApply with the same playlist, view, rows, gap and direction using --plan-token.\n", value.PlaylistID, value.TrackIDs, value.CanApply, value.PlanToken)
		return true, err
	case *playlist.ReorderResult:
		if agent.IsAgentMode() {
			_, err := fmt.Fprintf(output, "playlist_id:%d track_ids:%v\n", value.PlaylistID, value.TrackIDs)
			return true, err
		}
		_, err := fmt.Fprintf(output, "Reordered playlist %d. Saved track order: %v\n", value.PlaylistID, value.TrackIDs)
		return true, err
	}
	return false, nil
}
