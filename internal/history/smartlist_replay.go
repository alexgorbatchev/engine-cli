package history

import (
	"context"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type smartlistReplay struct {
	rows             []db.SmartlistRecord
	bad              []bool
	repair           []bool
	targets, parents []int64
	introduced       []int
}

// Replay each proven unlink before resolving its successor further. Resolving
// all targets to their final paths first loses the ordering between chains
// whose ordinary successors disappeared in different historical operations.
func (r smartlistReplay) apply(ctx context.Context, evidence *smartlistEvidence, orders smartlistOrderEvidence) error {
	rows, bad, targets, parents := r.rows, r.bad, r.targets, r.parents
	r.introduced = make([]int, len(rows))
	for i := range rows {
		if bad[i] {
			continue
		}
		r.introduced[i] = len(evidence.versions)
		for v := len(evidence.versions) - 1; v >= 0; v-- {
			if !r.sameHistoricalLink(ctx, evidence, i, v) {
				break
			}
			r.introduced[i] = v
		}
	}
	for v := 0; v+1 < len(orders); v++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		version := evidence.versions[v]
		if version.after || !evidence.versions[v+1].after || version.entryID != evidence.versions[v+1].entryID {
			continue
		}
		// Native rm/move unlinks one root from each affected sibling chain.
		// Multiple departures under one parent in a single image pair provide
		// no chronological order; do not choose Go's map iteration order.
		departures := make(map[int64][]int64)
		for id, before := range orders[v] {
			after, remains := orders[v+1][id]
			if !remains || after.parent != before.parent {
				departures[before.parent] = append(departures[before.parent], id)
			}
		}
		for parent, departed := range departures {
			for _, id := range departed {
				link := orders[v][id]
				valid := len(departed) == 1 && (version.operation == "playlist.rm" || version.operation == "playlist.move")
				if link.next != 0 {
					next, exists := orders[v][link.next]
					valid = valid && exists && next.parent == parent
				}
				r.unlink(id, link, valid, v)
			}
		}
	}
	for i := range rows {
		if bad[i] || rows[i].NextUUID != "" {
			continue
		}
		if targets[i] == 0 {
			rows[i].NextPath = ""
			continue
		}
		present := false
		for _, playlist := range evidence.current.Playlists {
			if playlist.ID == targets[i] && playlist.ParentID == parents[i] {
				rows[i].NextPath, present = playlist.Path, true
				break
			}
		}
		bad[i] = !present
	}
	return ctx.Err()
}

func (r smartlistReplay) unlink(id int64, link smartlistOrdinaryLink, valid bool, version int) {
	preceding := -1
	for i, row := range r.rows {
		if r.bad[i] || !r.repair[i] || version < r.introduced[i] || row.NextUUID != "" || r.targets[i] != id || r.parents[i] != link.parent {
			continue
		}
		if !valid || preceding >= 0 {
			r.bad[i] = true
			if preceding >= 0 {
				r.bad[preceding] = true
			}
		} else {
			preceding = i
		}
	}
	if preceding < 0 || r.bad[preceding] {
		return
	}
	// Use native identities as temporary anchor keys; paths may change across
	// these images and are resolved only at the end. Index once per unlink.
	anchors := append([]db.SmartlistRecord(nil), r.rows...)
	for i := range anchors {
		anchors[i].NextPath = strconv.FormatInt(r.targets[i], 10)
	}
	anchors[preceding].NextPath = strconv.FormatInt(link.next, 10)
	index := indexSmartlistSuccessors(anchors, r.bad)
	head, proven := index.head(preceding, anchors, func(i int) bool { return r.introduced[i] <= version })
	if !proven {
		r.bad[preceding] = true
		return
	}
	r.targets[preceding], r.rows[preceding].NextUUID = link.next, head
	if head != "" {
		r.rows[preceding].NextPath = ""
	}
}

// A departure can only repair linkage already held by this saved identity at
// that point and continuously retained afterwards. Later introduction, reuse,
// or an intervening different linkage cannot borrow an older unlink event.
func (r smartlistReplay) sameHistoricalLink(ctx context.Context, evidence *smartlistEvidence, row, version int) bool {
	inventory := evidence.versions[version].inventory
	if inventory == nil {
		return false
	}
	original := evidence.current.Saved[row]
	for _, historical := range inventory.Saved {
		if historical.UUID != original.UUID {
			continue
		}
		if historical.NextUUID != original.NextUUID {
			return false
		}
		parent, err := evidence.resolvePath(ctx, historical.ParentPath)
		originalParent, originalErr := evidence.resolvePath(ctx, original.ParentPath)
		if err != nil || originalErr != nil || parent == nil || originalParent == nil || parent.id != originalParent.id {
			return false
		}
		if original.NextUUID != "" {
			return historical.NextPath == original.NextPath
		}
		target, err := evidence.resolvePath(ctx, historical.NextPath)
		originalTarget, originalErr := evidence.resolvePath(ctx, original.NextPath)
		return err == nil && originalErr == nil && target != nil && originalTarget != nil && target.id == originalTarget.id
	}
	return false
}
