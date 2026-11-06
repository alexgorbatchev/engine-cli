package db

import (
	"context"
	"fmt"
)

// ValidateSmartlistInventory applies the native detector to an observed or
// proposed graph. Playlist identities are local to this inventory; the root is
// implicitly identity zero with an empty path. No stored values are rewritten.
func ValidateSmartlistInventory(ctx context.Context, inventory *SmartlistInventory) ([]SmartlistIssue, []SmartlistCause, error) {
	if inventory == nil {
		return nil, nil, fmt.Errorf("Smartlist inventory cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	paths := map[int64]string{0: ""}
	playlists := make([]playlistRow, 0, len(inventory.Playlists))
	for _, playlist := range inventory.Playlists {
		if _, exists := paths[playlist.ID]; exists {
			return nil, nil, fmt.Errorf("Smartlist inventory contains duplicate or root playlist identity")
		}
		paths[playlist.ID] = playlist.Path
		playlists = append(playlists, playlistRow{Playlist: Playlist{ID: playlist.ID, ParentListID: playlist.ParentID}})
	}
	saved := make([]savedSmartlist, 0, len(inventory.Saved))
	identities := make(map[string]bool, len(inventory.Saved))
	for _, row := range inventory.Saved {
		if identities[row.UUID] {
			return nil, nil, fmt.Errorf("Smartlist contains duplicate identities")
		}
		identities[row.UUID] = true
		saved = append(saved, savedSmartlist{id: row.UUID, name: row.Name, parent: row.ParentPath, next: row.NextPath, nextID: row.NextUUID})
	}
	return inspectSmartlistLinks(ctx, playlists, paths, saved)
}
