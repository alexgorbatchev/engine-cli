package main

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/duplicate"
	"github.com/spf13/cobra"
)

func newDuplicateScanCommand() *cobra.Command {
	var ids []int64
	var refs []string
	var jsonOutput bool
	c := &cobra.Command{Use: "scan", Short: "Fingerprint audio and group duplicate candidates", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if _, err := backup.AutoBackupIfNeeded(c.Context(), libDirFlag, noBackupFlag); err != nil {
			return err
		}
		return readDuplicates(func(d *sql.DB) error {
			r, err := duplicate.AudioGroups(c.Context(), d, libDirFlag, duplicate.AudioOptions{IDs: ids, Refs: refs, Generate: true})
			if err != nil {
				return err
			}
			return printAudioReport(c, r, jsonOutput)
		})
	}}
	c.Flags().Int64SliceVar(&ids, "ids", nil, "Track IDs to scan; omit to scan the whole library")
	c.Flags().StringSliceVar(&refs, "refs", nil, "Stable track references to scan")
	c.MarkFlagsMutuallyExclusive("ids", "refs")
	c.Flags().BoolVar(&jsonOutput, "json", false, "Return structured results as JSON")
	return c
}

func printAudioReport(c *cobra.Command, r *duplicate.AudioReport, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(c.OutOrStdout()).Encode(r)
	}
	if _, err := fmt.Fprintf(c.OutOrStdout(), "groups: %d\ntracks: %d\n", len(r.Groups), len(r.Items)); err != nil {
		return err
	}
	for _, g := range r.Groups {
		for _, t := range g.Tracks {
			if err := printDuplicate(c, t); err != nil {
				return err
			}
		}
	}
	for _, item := range r.Items {
		if item.Error != nil {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "id: %d error: %s message: %s\n", item.ID, item.Error.Code, displayPath(item.Error.Message)); err != nil {
				return err
			}
		}
	}
	return nil
}
