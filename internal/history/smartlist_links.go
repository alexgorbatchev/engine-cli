package history

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type smartlistLinkPlan struct {
	patches   []smartlistPatch
	deletions map[string][]db.SmartlistRecord
}

// Saved predecessor links are evidence of a complete native chain, not a sort
// preference. If a component has no provable order, remove its broken rows
// rather than inventing an ordering. Independent healthy chains stay untouched.
func planSmartlistLinks(ctx context.Context, evidence *smartlistEvidence, orders smartlistOrderEvidence) (*smartlistLinkPlan, error) {
	rows := evidence.current.Saved
	indices := make(map[string]int, len(rows))
	for i, row := range rows {
		indices[row.UUID] = i
	}
	decisions := make([]*smartlistParentDecision, len(rows))
	updated := make([]db.SmartlistRecord, len(rows))
	bad := make([]bool, len(rows))
	targets := make([]int64, len(rows))
	parentIDs := make([]int64, len(rows))
	repair := make([]bool, len(rows))
	for i, row := range rows {
		decision, err := classifySmartlistParent(ctx, evidence, row)
		if err != nil {
			return nil, err
		}
		decisions[i] = decision
		updated[i] = row
		updated[i].ParentPath = decision.parent
		bad[i] = decision.remove || row.UUID == "" || (row.NextUUID != "" && row.NextPath != "")
	}
	links := make([][]int, len(rows))
	connect := func(a, b int) {
		links[a] = append(links[a], b)
		links[b] = append(links[b], a)
	}
	anchors := make(map[[3]string]int)
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.NextUUID != "" {
			if next, exists := indices[row.NextUUID]; exists {
				connect(i, next)
				if decisions[i].parent != decisions[next].parent {
					bad[i] = true
				}
			} else {
				bad[i] = true
			}
		} else if row.NextPath != "" && !bad[i] {
			next, err := evidence.resolvePath(ctx, row.NextPath)
			if err != nil && progress.ErrorCode(err) != "smartlist_repair_ambiguous" {
				return nil, err
			}
			parent, parentErr := evidence.resolvePath(ctx, row.ParentPath)
			if parentErr != nil && progress.ErrorCode(parentErr) != "smartlist_repair_ambiguous" {
				return nil, parentErr
			}
			if err != nil || parentErr != nil || next == nil || parent == nil {
				bad[i] = true
			} else {
				targets[i], parentIDs[i] = next.id, parent.id
				repair[i] = true
				for _, playlist := range evidence.current.Playlists {
					if playlist.ID == next.id && playlist.ParentID == parent.id {
						repair[i] = false
					}
				}
			}
		}
	}
	replay := smartlistReplay{rows: updated, bad: bad, targets: targets, parents: parentIDs, repair: repair}
	if err := replay.apply(ctx, evidence, orders); err != nil {
		return nil, err
	}
	for i, row := range updated {
		if next, exists := indices[row.NextUUID]; row.NextUUID != "" && exists {
			connect(i, next)
		}
	}
	for i := range rows {
		if !bad[i] {
			key := [3]string{updated[i].ParentPath, updated[i].NextPath, updated[i].NextUUID}
			if previous, exists := anchors[key]; exists {
				connect(i, previous)
				bad[i], bad[previous] = true, true
			} else {
				anchors[key] = i
			}
		}
	}
	// Peel acyclic vertices from the functional next graph. Any remaining vertex
	// lies on a cycle; component propagation below includes its predecessors.
	indegree := make([]int, len(rows))
	for _, row := range updated {
		if next, exists := indices[row.NextUUID]; row.NextUUID != "" && exists {
			indegree[next]++
		}
	}
	queue := make([]int, 0, len(rows))
	for i, degree := range indegree {
		if degree == 0 {
			queue = append(queue, i)
		}
	}
	for head := 0; head < len(queue); head++ {
		i := queue[head]
		if next, exists := indices[updated[i].NextUUID]; updated[i].NextUUID != "" && exists {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	for i, degree := range indegree {
		bad[i] = bad[i] || degree > 0
	}
	seen := make([]bool, len(rows))
	plan := &smartlistLinkPlan{deletions: make(map[string][]db.SmartlistRecord)}
	for start := range rows {
		if seen[start] {
			continue
		}
		queue = append(queue[:0], start)
		seen[start] = true
		remove := false
		for head := 0; head < len(queue); head++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			i := queue[head]
			remove = remove || bad[i]
			for _, next := range links[i] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		for _, i := range queue {
			if remove {
				plan.patches = append(plan.patches, smartlistPatch{before: rows[i]})
				if entry := decisions[i].deleteEntry; entry != "" {
					plan.deletions[entry] = append(plan.deletions[entry], rows[i])
				}
			} else if updated[i] != rows[i] {
				after := updated[i]
				plan.patches = append(plan.patches, smartlistPatch{before: rows[i], after: &after})
			}
		}
	}
	return plan, nil
}
