package rekordbox

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// writeHierarchy preserves first-occurrence order. Engine's native uniqueness
// constraint cannot represent repeated members; review must disclose them.
func writeHierarchy(ctx context.Context, tx *sql.Tx, parent int64, nodes []Playlist, tracks map[string]int64) error {
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := db.CreatePlaylist(ctx, tx, node.Name, parent, node.Folder)
		if err != nil {
			return err
		}
		for _, selector := range node.Tracks {
			trackID, ok := tracks[selector]
			if !ok {
				return fmt.Errorf("source membership has no reviewed track disposition")
			}
			if trackID == 0 {
				continue // Zero is an explicitly reviewed missing-media omission.
			}
			if _, err := db.AddTrackToPlaylist(ctx, tx, id, trackID); err != nil {
				return err
			}
		}
		if err := writeHierarchy(ctx, tx, id, node.Children, tracks); err != nil {
			return err
		}
	}
	return nil
}

func hierarchyWarnings(nodes []Playlist) []Warning {
	warnings := []Warning{}
	for _, node := range nodes {
		if node.Folder && len(node.Children) == 0 {
			warnings = append(warnings, Warning{Code: sourceOnlyCode, Field: "EmptyFolder"})
		}
		seen := map[string]bool{}
		for _, selector := range node.Tracks {
			if seen[selector] {
				warnings = append(warnings, Warning{Code: sourceOnlyCode, SourceID: selector, Field: "RepeatedMembership"})
			}
			seen[selector] = true
		}
		warnings = append(warnings, hierarchyWarnings(node.Children)...)
	}
	return warnings
}
