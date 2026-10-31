package db

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// OrderPlaylistMemberships validates and follows native PlaylistEntity links.
// Unresolved track origins still belong in links: dropping them can hide a
// broken chain or change the placement of the remaining memberships.
func OrderPlaylistMemberships(links map[int64]int64) ([]int64, error) {
	invalid := func(message string) error {
		return &progress.Error{Code: "playlist_membership_order_invalid", Cause: fmt.Errorf("%s", message)}
	}
	incoming := make(map[int64]bool, len(links))
	for id, next := range links {
		if id <= 0 || next < 0 {
			return nil, invalid("playlist has an invalid membership identity")
		}
		if next == 0 {
			continue
		}
		if _, exists := links[next]; !exists || incoming[next] {
			return nil, invalid("playlist has a dangling or branching membership link")
		}
		incoming[next] = true
	}
	var head int64
	for id := range links {
		if incoming[id] {
			continue
		}
		if head != 0 {
			return nil, invalid("playlist has multiple saved membership heads")
		}
		head = id
	}
	ordered := make([]int64, 0, len(links))
	seen := make(map[int64]bool, len(links))
	for head != 0 && !seen[head] {
		seen[head] = true
		ordered = append(ordered, head)
		head = links[head]
	}
	if len(ordered) != len(links) {
		return nil, invalid("playlist contains cyclic membership links")
	}
	return ordered, nil
}
