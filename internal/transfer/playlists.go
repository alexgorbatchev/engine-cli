package transfer

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

func (p *plan) applyPlaylists(ctx context.Context, c *sql.Conn) error {
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
	// Rebuild each affected sibling chain, retaining destination-only siblings.
	parents := map[string]bool{}
	for _, e := range p.desired {
		if e.Kind == "playlist" {
			parents[e.Parent] = true
			if old := p.destination.Entities[e.Key]; old != nil {
				parents[old.Parent] = true
			}
		}
	}
	for parent := range parents {
		siblings := map[string]*entity{}
		for k, e := range p.destination.Entities {
			if e.Kind == "playlist" && e.Parent == parent && !p.remove[k] {
				siblings[k] = e
			}
		}
		for k, e := range p.desired {
			if e.Kind == "playlist" {
				delete(siblings, k)
				if e.Parent == parent {
					siblings[k] = e
				}
			}
		}
		order := []string{}
		seen := map[string]bool{}
		var appendChain func(string)
		appendChain = func(k string) {
			for e := siblings[k]; e != nil && !seen[k]; e = siblings[k] {
				seen[k] = true
				order = append(order, k)
				k = e.Next
			}
		}
		pointed := map[string]bool{}
		for _, e := range siblings {
			pointed[e.Next] = true
		}
		for _, k := range orderedKeys(siblings) {
			if !pointed[k] {
				appendChain(k)
			}
		}
		for _, k := range orderedKeys(siblings) {
			appendChain(k)
		}
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
