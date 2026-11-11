package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// Resolve the ordinary successor after unlinking the source. A chain that
// previously ended at the source now continues through its former successor.
func smartlistFollowingPlaylist(ctx context.Context, tx *sql.Tx, uuid string, parentID int64, paths map[int64]string, playlists []playlistRow) (int64, error) {
	invalid := func() error {
		return &progress.Error{Code: "playlist_move_invalid", Cause: fmt.Errorf("Smartlist insertion sibling is missing or has invalid links")}
	}
	exists, err := hasSmartlistTable(ctx, tx)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, invalid()
	}
	seen := make(map[string]bool)
	for uuid != "" {
		if seen[uuid] {
			return 0, invalid()
		}
		seen[uuid] = true
		var parent, next, nextUUID string
		err := tx.QueryRowContext(ctx, `SELECT parentPlaylistPath,nextPlaylistPath,nextListUuid FROM Smartlist WHERE listUuid=?`, uuid).Scan(&parent, &next, &nextUUID)
		if err == sql.ErrNoRows {
			return 0, invalid()
		}
		if err != nil {
			return 0, err
		}
		if parent != paths[parentID] || (next != "" && nextUUID != "") {
			return 0, invalid()
		}
		if nextUUID != "" {
			uuid = nextUUID
			continue
		}
		if next == "" {
			return 0, nil
		}
		var matches []int64
		for _, playlist := range playlists {
			if playlist.ParentListID == parentID && paths[playlist.ID] == next {
				matches = append(matches, playlist.ID)
			}
		}
		if len(matches) != 1 {
			return 0, invalid()
		}
		return matches[0], nil
	}
	return 0, invalid()
}
