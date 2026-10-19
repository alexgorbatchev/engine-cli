package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dj/engine-cli/internal/transfer"
	"github.com/spf13/cobra"
)

func newLibraryTransferCommand() *cobra.Command {
	var o transfer.Options
	var asJSON bool
	var mappings []string
	c := &cobra.Command{Use: "sync", Short: "Sync playlists or a whole library to another library", Args: cobra.NoArgs,
		Long: "Sync from an explicit source library to a destination library. Select source playlist IDs or --root. Use --dry-run to preview without writing anything. Destination-only content is retained unless --mirror is set. Close Engine DJ before applying changes.",
		RunE: func(c *cobra.Command, _ []string) error {
			for _, mapping := range mappings {
				a, b, ok := strings.Cut(mapping, ":")
				source, e1 := strconv.ParseInt(a, 10, 64)
				dest, e2 := strconv.ParseInt(b, 10, 64)
				if !ok || e1 != nil || e2 != nil || source <= 0 || dest <= 0 {
					return fmt.Errorf("map-playlist must be source-id:destination-id")
				}
				o.PlaylistMap = append(o.PlaylistMap, transfer.PlaylistMapping{SourceID: source, DestinationID: dest})
			}
			o.NoBackup = noBackupFlag
			r, err := transfer.Run(c.Context(), o)
			if r != nil {
				if asJSON {
					if e := json.NewEncoder(c.OutOrStdout()).Encode(r); e != nil {
						return e
					}
				} else {
					if _, e := fmt.Fprintf(c.OutOrStdout(), "applicable: %t\ndry_run: %t\nplan_token: %s\nfiles_to_copy: %d\nbytes_to_copy: %d\nconflicts: %d\n", r.Applicable, o.DryRun, r.PlanToken, r.Summary.FilesCopied, r.Summary.BytesToCopy, len(r.Conflicts)); e != nil {
						return e
					}
					for _, change := range append(r.Changes, r.Conflicts...) {
						if _, e := fmt.Fprintf(c.OutOrStdout(), "%s: %s id: %s source_id: %d destination_id: %d reason: %s\n", change.Action, change.Entity, change.ID, change.SourceID, change.DestinationID, change.Reason); e != nil {
							return e
						}
					}
					if r.OperationID != "" {
						if _, e := fmt.Fprintf(c.OutOrStdout(), "operation_id: %s\n", r.OperationID); e != nil {
							return e
						}
					}
				}
			}
			return err
		}}
	f := c.Flags()
	f.StringVar(&o.Source, "from", "", "Source Engine Library path")
	f.StringVar(&o.Destination, "to", "", "Destination Engine Library path")
	f.Int64SliceVar(&o.Scope.PlaylistIDs, "playlist-id", nil, "Source playlist IDs, including descendants (repeatable)")
	f.StringSliceVar(&o.Scope.PlaylistRefs, "playlist-ref", nil, "Stable source playlist references, including descendants")
	f.BoolVar(&o.Scope.Root, "root", false, "Sync the whole library, including tracks outside playlists")
	f.BoolVar(&o.DryRun, "dry-run", false, "Preview changes and conflicts without writing to either library")
	f.BoolVar(&o.Mirror, "mirror", false, "Remove destination entries absent from the selected source scope")
	f.StringVar(&o.Conflict, "conflict", "error", "Resolve conflicting edits: error, source, or destination")
	f.StringVar(&o.Media, "media", "changed", "Audio copying: changed or none")
	f.StringVar(&o.PlanToken, "plan-token", "", "Require the library state to match a previous preview token")
	f.StringSliceVar(&mappings, "map-playlist", nil, "Explicitly match source-id:destination-id for existing playlists (repeatable)")
	f.BoolVar(&asJSON, "json", false, "Print the preview or result as JSON")
	c.MarkFlagsRequiredTogether("from", "to")
	c.MarkFlagsMutuallyExclusive("root", "playlist-id", "playlist-ref")
	c.MarkFlagsOneRequired("root", "playlist-id", "playlist-ref")
	return c
}
