package transfer

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

func (p *plan) applyPlaylists(ctx context.Context, c *sql.Tx) error {
	ids := map[string]int64{}
	for id, k := range p.destination.Playlists {
		ids[k] = id
	}
	pending := map[string]*entity{}
	for k, e := range p.desired {
		if e.Kind == "playlist" {
			pending[k] = e
		}
	}
	for len(pending) > 0 {
		advanced := false
		for _, key := range orderedKeys(pending) {
			e := pending[key]
			if e.Parent != "" && ids[e.Parent] == 0 {
				continue
			}
			v := clone(e.Values)
			v["parentListId"] = ids[e.Parent]
			v["nextListId"] = nil
			var id any
			if ids[key] != 0 {
				id = ids[key]
			}
			n, err := writeRow(ctx, c, "Playlist", v, "id", id)
			if err != nil {
				return err
			}
			if id != nil {
				n = ids[key]
			}
			ids[key] = n
			p.destination.Playlists[n] = key
			p.destination.State.Playlists[strconv.FormatInt(n, 10)] = key
			if _, err = c.ExecContext(ctx, "DELETE FROM PlaylistEntity WHERE listId=?", n); err != nil {
				return err
			}
			var next int64
			for i := len(e.Members) - 1; i >= 0; i-- {
				track := p.membershipTrack(e.Members[i])
				if track == nil {
					return fmt.Errorf("missing playlist track identity")
				}
				next, err = writeRow(ctx, c, "PlaylistEntity", row{"listId": n, "trackId": track.Values["originTrackId"], "databaseUuid": track.Values["originDatabaseUuid"], "nextEntityId": next, "membershipReference": int64(0)}, "id", nil)
				if err != nil {
					return err
				}
			}
			delete(pending, key)
			advanced = true
		}
		if !advanced {
			return fmt.Errorf("playlist hierarchy contains a cycle or missing parent")
		}
	}
	// Publish the exact ordinary order reviewed by the planner.
	for _, parent := range slices.Sorted(maps.Keys(p.playlistOrders)) {
		order := p.playlistOrders[parent]
		if _, err := c.ExecContext(ctx, "UPDATE Playlist SET nextListId=NULL WHERE parentListId=?", ids[parent]); err != nil {
			return err
		}
		for i, k := range order {
			var next int64
			if i+1 < len(order) {
				next = ids[order[i+1]]
			}
			if _, err := c.ExecContext(ctx, "UPDATE Playlist SET nextListId=? WHERE id=?", next, ids[k]); err != nil {
				return err
			}
		}
	}
	return nil
}
