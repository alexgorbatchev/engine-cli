package transfer

import (
	"context"
	"fmt"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// validateSmartlists runs after native triggers on the same uncommitted write
// connection. Healthy-but-unreviewed changes must fail alongside broken links.
func (p *plan) validateSmartlists(ctx context.Context, reader queryer) error {
	inventory, err := db.ReadSmartlistInventoryFrom(ctx, reader)
	if err != nil {
		return err
	}
	if len(inventory.Issues) != 0 {
		return fmt.Errorf("sync produced invalid destination Smartlist links")
	}
	actual, _, err := rows(ctx, reader, "Smartlist")
	if err != nil {
		return err
	}
	if len(actual) != len(p.expectedSmartlists) {
		return fmt.Errorf("destination Smartlist rows differ from the reviewed plan")
	}
	for _, value := range actual {
		expected := p.expectedSmartlists["smartlist:"+str(value["listUuid"])]
		if expected == nil || !reflect.DeepEqual(without(value, "lastEditTime"), expected.Values) {
			return fmt.Errorf("destination Smartlist values differ from the reviewed plan")
		}
	}
	paths, err := plannedPlaylistPaths(ctx, p.finalEntities)
	if err != nil {
		return err
	}
	if len(inventory.Playlists) != len(paths)-1 {
		return fmt.Errorf("destination playlist graph differs from the reviewed plan")
	}
	for _, playlist := range inventory.Playlists {
		key := p.destination.Playlists[playlist.ID]
		if path, exists := paths[key]; !exists || key == "" || path != playlist.Path {
			return fmt.Errorf("destination playlist paths differ from the reviewed plan")
		}
		if p.finalEntities[key].Parent != p.destination.Playlists[playlist.ParentID] {
			return fmt.Errorf("destination playlist parent identity differs from the reviewed plan")
		}
	}
	return p.validatePlaylistOrders(ctx, reader)
}

func (p *plan) validatePlaylistOrders(ctx context.Context, reader queryer) error {
	values, _, err := rows(ctx, reader, "Playlist")
	if err != nil {
		return err
	}
	next := map[string]string{}
	for _, value := range values {
		key := p.destination.Playlists[number(value["id"])]
		nextID := number(value["nextListId"])
		next[key] = p.destination.Playlists[nextID]
		if nextID != 0 && next[key] == "" {
			return fmt.Errorf("destination playlist order references an unknown identity")
		}
	}
	for key, expected := range p.finalEntities {
		if expected.Kind == "playlist" {
			if actual, exists := next[key]; !exists || actual != expected.Next {
				return fmt.Errorf("destination playlist order differs from the reviewed plan")
			}
		}
	}
	return nil
}
