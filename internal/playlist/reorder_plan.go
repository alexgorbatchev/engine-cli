package playlist

import (
	"fmt"
	"slices"
)

// IDs identify PlaylistEntity rows, never local Track rows. The database owner
// resolves origin pairs before constructing this immutable planning input.
type orderMove struct {
	view       []int64
	rows       []int
	drop       int
	descending bool
}

type rowRange struct {
	first int
	last  int
}

type orderLink struct {
	previous int
	next     int
}

func moveOrder(chain []int64, move orderMove) ([]int64, error) {
	if err := validateOrderView(chain, move); err != nil {
		return nil, err
	}
	ranges, err := selectedRanges(move)
	if err != nil {
		return nil, err
	}
	next := orderDropTarget(chain, move, ranges)
	return spliceOrder(chain, move, ranges, next)
}

func spliceOrder(chain []int64, move orderMove, ranges []rowRange, next int64) ([]int64, error) {
	// Index zero is the circular chain sentinel. Native entity IDs remain
	// stable indexes while each selected run changes only six adjacent links.
	positions := make(map[int64]int, len(chain))
	links := make([]orderLink, len(chain)+1)
	for i, id := range chain {
		positions[id] = i + 1
		links[i+1] = orderLink{previous: i, next: (i + 2) % len(links)}
	}
	links[0] = orderLink{previous: len(chain), next: 1}
	target := positions[next]
	for _, span := range ranges {
		first, last := move.view[span.first], move.view[span.last]
		if move.descending {
			first, last = last, first
		}
		start, end := positions[first], positions[last]
		// A contiguous shown run includes the hidden memberships between its
		// endpoints, matching the native linked-entity splice.
		if links[end].next == target {
			continue
		}
		if start <= target && target <= end {
			return nil, fmt.Errorf("drop target is inside the moved membership run")
		}
		previous, after := links[start].previous, links[end].next
		links[previous].next = after
		links[after].previous = previous
		beforeTarget := links[target].previous
		links[beforeTarget].next = start
		links[start].previous = beforeTarget
		links[end].next = target
		links[target].previous = end
	}
	ordered := make([]int64, 0, len(chain))
	for node := links[0].next; node != 0; node = links[node].next {
		ordered = append(ordered, chain[node-1])
	}
	return ordered, nil
}

func validateOrderView(chain []int64, move orderMove) error {
	if move.drop < 0 || move.drop > len(move.view) {
		return fmt.Errorf("drop gap is outside the shown rows")
	}
	positions := make(map[int64]int, len(chain))
	for i, id := range chain {
		if _, exists := positions[id]; exists || id <= 0 {
			return fmt.Errorf("saved membership order has invalid entity identities")
		}
		positions[id] = i
	}
	previous := -1
	if move.descending {
		previous = len(chain)
	}
	for _, id := range move.view {
		position, exists := positions[id]
		if !exists || !move.descending && position <= previous || move.descending && position >= previous {
			return fmt.Errorf("shown rows do not match the saved membership order")
		}
		previous = position
	}
	return nil
}

func selectedRanges(move orderMove) ([]rowRange, error) {
	if len(move.rows) == 0 {
		return nil, fmt.Errorf("select at least one shown row")
	}
	rows := slices.Clone(move.rows)
	slices.Sort(rows)
	ranges := make([]rowRange, 0, len(rows))
	for _, row := range rows {
		if row < 0 || row >= len(move.view) {
			return nil, fmt.Errorf("selected row is not shown")
		}
		if len(ranges) > 0 {
			last := &ranges[len(ranges)-1]
			if last.last == row {
				return nil, fmt.Errorf("selected row appears more than once")
			}
			if last.last+1 == row {
				last.last = row
				continue
			}
		}
		ranges = append(ranges, rowRange{first: row, last: row})
	}
	if move.descending {
		slices.Reverse(ranges)
	}
	return ranges, nil
}

func orderDropTarget(chain []int64, move orderMove, ranges []rowRange) int64 {
	dropped := move.drop
	if move.descending {
		dropped--
	}
	target := dropped
	for _, span := range ranges {
		if span.first <= dropped && dropped <= span.last {
			target = span.last + 1
			if move.descending {
				target = span.first - 1
			}
			break
		}
	}
	var next int64
	if target >= 0 && target < len(move.view) {
		next = move.view[target]
	}
	shown := make(map[int64]bool, len(move.view))
	for _, id := range move.view {
		shown[id] = true
	}
	for _, span := range ranges {
		if span.first == dropped {
			pivot := move.view[dropped]
			if move.descending {
				pivot = next
			}
			place := beforeHiddenOrder(chain, shown, pivot)
			if move.descending || place != move.view[dropped] {
				return place
			}
		}
	}
	if move.descending {
		return next
	}
	return beforeHiddenOrder(chain, shown, next)
}

func beforeHiddenOrder(chain []int64, shown map[int64]bool, pivot int64) int64 {
	position := len(chain)
	if pivot != 0 {
		position = slices.Index(chain, pivot)
	}
	for position > 0 && !shown[chain[position-1]] {
		position--
	}
	if position == len(chain) {
		return 0
	}
	return chain[position]
}
