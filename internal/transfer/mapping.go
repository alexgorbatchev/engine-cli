package transfer

import (
	"fmt"
	"strconv"
)

func mapPlaylists(mappings []PlaylistMapping, s, d *collection) error {
	replacements := map[string]string{}
	seen := map[string]bool{}
	for _, m := range mappings {
		source, dest := s.Playlists[m.SourceID], d.Playlists[m.DestinationID]
		if source == "" || dest == "" {
			return fmt.Errorf("playlist mapping requires existing source and destination IDs")
		}
		if seen[source] || replacements[dest] != "" {
			return fmt.Errorf("playlist mapping must be one-to-one")
		}
		if source != dest && d.Entities[source] != nil {
			return fmt.Errorf("source playlist is already mapped to another destination playlist")
		}
		seen[source] = true
		replacements[dest] = source
	}
	for old, key := range replacements {
		e := d.Entities[old]
		delete(d.Entities, old)
		e.Key = key
		d.Entities[key] = e
		d.Playlists[e.ID] = key
		d.State.Playlists[strconv.FormatInt(e.ID, 10)] = key
	}
	for _, e := range d.Entities {
		if key := replacements[e.Parent]; key != "" {
			e.Parent = key
		}
		if key := replacements[e.Next]; key != "" {
			e.Next = key
		}
	}
	return nil
}
