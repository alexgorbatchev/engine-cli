package transfer

// planPlaylistOrders owns the same ordinary sibling merge used by publication.
// Saved chains are validated separately; this ordering never resolves ambiguous
// saved predecessors by sorting their identities.
func (p *plan) planPlaylistOrders() {
	p.playlistOrders = map[string][]string{}
	parents := map[string]bool{}
	for key, e := range p.desired {
		if e.Kind == "playlist" {
			parents[e.Parent] = true
			if old := p.destination.Entities[key]; old != nil {
				parents[old.Parent] = true
			}
		}
	}
	for key := range p.remove {
		if old := p.destination.Entities[key]; old != nil && old.Kind == "playlist" {
			parents[old.Parent] = true
		}
	}
	for parent := range parents {
		if parent != "" && p.finalEntities[parent] == nil {
			continue
		}
		siblings := map[string]*entity{}
		for key, e := range p.finalEntities {
			if e.Kind == "playlist" && e.Parent == parent {
				siblings[key] = e
			}
		}
		order := mergedPlaylistOrder(siblings)
		p.playlistOrders[parent] = order
		for index, key := range order {
			updated := *p.finalEntities[key]
			updated.Next = ""
			if index+1 < len(order) {
				updated.Next = order[index+1]
			}
			p.finalEntities[key] = &updated
		}
	}
}

func mergedPlaylistOrder(siblings map[string]*entity) []string {
	order := []string{}
	seen := map[string]bool{}
	appendChain := func(key string) {
		for e := siblings[key]; e != nil && !seen[key]; e = siblings[key] {
			seen[key] = true
			order = append(order, key)
			key = e.Next
		}
	}
	pointed := map[string]bool{}
	for _, e := range siblings {
		pointed[e.Next] = true
	}
	for _, key := range orderedKeys(siblings) {
		if !pointed[key] {
			appendChain(key)
		}
	}
	for _, key := range orderedKeys(siblings) {
		appendChain(key)
	}
	return order
}
