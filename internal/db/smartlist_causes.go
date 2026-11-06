package db

import (
	"cmp"
	"context"
	"slices"
)

// SmartlistCause retains the exact failed relationship behind a public issue.
// Historical repair must compare these witnesses, not merely issue labels.
// Members is used once per colliding anchor, preserving every predecessor's
// identity without storing a quadratic list of pairwise collisions.
type SmartlistCause struct {
	UUID, Kind, Reason                    string
	ParentPath, NextPath, NextUUID        string
	TargetParent                          string
	ParentIDs, TargetIDs, TargetParentIDs []int64
	Members                               []string
}

func inspectSmartlistLinks(ctx context.Context, playlists []playlistRow, paths map[int64]string, saved []savedSmartlist) ([]SmartlistIssue, []SmartlistCause, error) {
	// Native titles contain unescaped semicolons; one path can name multiple IDs.
	pathIDs := make(map[string][]int64, len(paths))
	for id, path := range paths {
		pathIDs[path] = append(pathIDs[path], id)
	}
	for _, ids := range pathIDs {
		slices.Sort(ids)
	}
	parents := make(map[int64]int64, len(playlists))
	for _, p := range playlists {
		parents[p.ID] = p.ParentListID
	}
	byID := make(map[string]savedSmartlist, len(saved))
	for _, row := range saved {
		byID[row.id] = row
	}
	issues := []SmartlistIssue{}
	causes := []SmartlistCause{}
	seenIssue := make(map[[2]string]bool)
	issue := func(row savedSmartlist, kind string) {
		key := [2]string{row.id, kind}
		if !seenIssue[key] {
			seenIssue[key] = true
			issues = append(issues, SmartlistIssue{UUID: row.id, Name: row.name, Kind: kind})
		}
	}
	add := func(row savedSmartlist, kind, reason string) *SmartlistCause {
		issue(row, kind)
		causes = append(causes, SmartlistCause{UUID: row.id, Kind: kind, Reason: reason,
			ParentPath: row.parent, NextPath: row.next, NextUUID: row.nextID})
		return &causes[len(causes)-1]
	}
	predecessors := make(map[[3]string][]string)
	for _, row := range saved {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		parentIDs := pathIDs[row.parent]
		if len(parentIDs) != 1 {
			add(row, "parent_path_unresolved", "parent_identity").ParentIDs = parentIDs
		}
		if row.id == "" {
			add(row, "identity_invalid", "empty_identity")
		}
		if row.nextID != "" {
			next, ok := byID[row.nextID]
			if !ok {
				add(row, "sibling_link_invalid", "saved_target_missing")
			} else if next.parent != row.parent {
				add(row, "sibling_link_invalid", "saved_target_parent").TargetParent = next.parent
			}
			if row.next != "" {
				add(row, "sibling_link_invalid", "dual_successors")
			}
		} else if row.next != "" {
			nextIDs := pathIDs[row.next]
			if len(nextIDs) != 1 || len(parentIDs) != 1 || parents[nextIDs[0]] != parentIDs[0] {
				cause := add(row, "next_path_unresolved", "ordinary_target_identity")
				cause.ParentIDs, cause.TargetIDs = parentIDs, nextIDs
				for _, id := range nextIDs {
					cause.TargetParentIDs = append(cause.TargetParentIDs, parents[id])
				}
			}
		}
		key := [3]string{row.parent, row.next, row.nextID}
		predecessors[key] = append(predecessors[key], row.id)
		// Preserve the detector's public issue order independently of the richer
		// aggregate collision witness built below.
		if members := predecessors[key]; len(members) > 1 {
			issue(byID[members[0]], "sibling_link_invalid")
			issue(row, "sibling_link_invalid")
		}
	}
	anchors := make([][3]string, 0, len(predecessors))
	for anchor := range predecessors {
		anchors = append(anchors, anchor)
	}
	slices.SortFunc(anchors, func(a, b [3]string) int {
		for i := range a {
			if order := cmp.Compare(a[i], b[i]); order != 0 {
				return order
			}
		}
		return 0
	})
	for _, anchor := range anchors {
		members := predecessors[anchor]
		if len(members) > 1 {
			slices.Sort(members)
			causes = append(causes, SmartlistCause{Kind: "sibling_link_invalid", Reason: "predecessor_collision",
				ParentPath: anchor[0], NextPath: anchor[1], NextUUID: anchor[2], Members: members})
		}
	}
	// Mark each exact directed cycle edge. Counts or unordered cycle membership
	// cannot prove that an amendment preserved the original relationships.
	finished := make(map[string]bool, len(saved))
	for _, row := range saved {
		chain := []string{}
		positions := make(map[string]int)
		id := row.id
		for id != "" && !finished[id] {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if start, ok := positions[id]; ok {
				for _, cyclic := range chain[start:] {
					add(byID[cyclic], "sibling_cycle", "cycle_edge")
				}
				break
			}
			next, ok := byID[id]
			if !ok {
				break
			}
			positions[id] = len(chain)
			chain = append(chain, id)
			id = next.nextID
		}
		for _, visited := range chain {
			finished[visited] = true
		}
	}
	return issues, causes, nil
}
