package main

import (
	"encoding/json"
	"fmt"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/details"
	"github.com/spf13/cobra"
	"path/filepath"
	"strconv"
	"strings"
)

func newDetailsCommand(kind string) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "inspect <id...>", Short: "Read details for one or more " + kind + " IDs", Args: cobra.RangeArgs(1, details.MaxItems), RunE: func(c *cobra.Command, args []string) error {
		ids := make([]int64, len(args))
		useRefs := strings.HasPrefix(args[0], "eng:")
		for i, arg := range args {
			if useRefs {
				continue
			}
			n, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				return fmt.Errorf("use positive numeric IDs: %q", arg)
			}
			ids[i] = n
		}
		if !useRefs {
			if err := details.Validate(ids); err != nil {
				return err
			}
		}
		d, err := db.Open(filepath.Join(libDirFlag, "Database2", "m.db"), true)
		if err != nil {
			return err
		}
		defer d.Close()
		var r *details.Report
		if useRefs {
			r, err = details.ReadRefs(c.Context(), d, kind, args)
		} else {
			r, err = details.Read(c.Context(), d, kind, ids)
		}
		if err != nil {
			return err
		}
		if asJSON {
			for _, item := range r.Items {
				foldTrackPath(item.Data)
				if tracks, ok := item.Data["tracks"].([]map[string]any); ok {
					for _, track := range tracks {
						foldTrackPath(track)
					}
				}
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(r)
		}
		for _, item := range r.Items {
			if item.Error != nil {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "%s: %d error: %s\n", kind, item.ID, item.Error.Code); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(c.OutOrStdout(), "%s: %d title: %s\n", kind, item.ID, item.Data["title"]); err != nil {
				return err
			}
			if kind == "playlist" {
				for _, track := range item.Data["tracks"].([]map[string]any) {
					if _, err := fmt.Fprintf(c.OutOrStdout(), "  track: %v artist: %v title: %v\n", track["id"], track["artist"], track["title"]); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}}
	c.Flags().BoolVar(&asJSON, "json", false, "Print ordered details and per-item errors as JSON")
	return c
}

func foldTrackPath(data map[string]any) {
	if path, ok := data["path"].(string); ok {
		data["path"] = displayPath(path)
	}
}
