package transfer

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func plannedPlaylistPaths(ctx context.Context, entities map[string]*entity) (map[string]string, error) {
	paths := map[string]string{"": ""}
	visiting := map[string]bool{}
	var visit func(string) (string, error)
	visit = func(key string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if path, exists := paths[key]; exists {
			return path, nil
		}
		e := entities[key]
		if e == nil || e.Kind != "playlist" {
			return "", fmt.Errorf("final playlist parent identity %q is absent", key)
		}
		if visiting[key] {
			return "", fmt.Errorf("final playlist hierarchy contains a cycle")
		}
		visiting[key] = true
		parent, err := visit(e.Parent)
		if err != nil {
			return "", err
		}
		paths[key] = parent + str(e.Values["title"]) + ";"
		delete(visiting, key)
		return paths[key], nil
	}
	for _, key := range orderedKeys(entities) {
		if entities[key].Kind == "playlist" {
			if _, err := visit(key); err != nil {
				return nil, err
			}
		}
	}
	return paths, nil
}

func plannedSmartlistInventory(entities map[string]*entity, paths map[string]string) *db.SmartlistInventory {
	inventory := &db.SmartlistInventory{HasSmartlist: true}
	// These unique graph handles exist only during validation. They are never
	// destination native IDs and cannot be persisted or used as write selectors.
	ids := map[string]int64{"": 0}
	for _, key := range slices.Sorted(maps.Keys(paths)) {
		if key != "" {
			ids[key] = int64(len(ids))
		}
	}
	for _, key := range orderedKeys(entities) {
		e := entities[key]
		if e.Kind == "playlist" {
			inventory.Playlists = append(inventory.Playlists, db.SmartlistPlaylist{ID: ids[key], ParentID: ids[e.Parent], Path: paths[key]})
		}
		if e.Kind == "smartlist" {
			inventory.Saved = append(inventory.Saved, db.SmartlistRecord{UUID: str(e.Values["listUuid"]), Name: str(e.Values["title"]), ParentPath: str(e.Values["parentPlaylistPath"]), NextPath: str(e.Values["nextPlaylistPath"]), NextUUID: str(e.Values["nextListUuid"])})
		}
	}
	return inventory
}

// Follow the chosen row's observed mixed chain when an ordinary anchor departs
// or a saved successor is explicitly removed. Surviving saved rows are retained;
// merging chains with competing predecessors is left to the shared detector.
func (p *plan) smartlistSuccessor(ctx context.Context, origin savedTopology, saved *entity) (string, error) {
	next := saved.Next
	seen := map[string]bool{}
	for next != "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if seen[next] {
			return "", fmt.Errorf("saved successor traversal contains a cycle")
		}
		seen[next] = true
		if current := p.finalEntities[next]; current != nil && current.Parent == saved.Parent && (current.Kind == "playlist" || current.Kind == "smartlist") {
			return next, nil
		}
		old := origin.entities[next]
		if old == nil || old.Parent != saved.Parent {
			return "", fmt.Errorf("saved successor has no unambiguous original sibling identity")
		}
		if old.Kind == "smartlist" {
			next = old.Next
			continue
		}
		if old.Kind != "playlist" {
			return "", fmt.Errorf("saved successor is not a playlist or Smartlist")
		}
		var err error
		next, err = origin.head(ctx, saved.Parent, old.Next)
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

type savedTopology struct {
	entities     map[string]*entity
	predecessors map[[2]string][]string
}

func savedTopologyFrom(origin *collection) savedTopology {
	result := savedTopology{entities: origin.Entities, predecessors: map[[2]string][]string{}}
	for key, e := range origin.Entities {
		if e.Kind == "smartlist" {
			anchor := [2]string{e.Parent, e.Next}
			result.predecessors[anchor] = append(result.predecessors[anchor], key)
		}
	}
	return result
}

func (origin savedTopology) head(ctx context.Context, parent, next string) (string, error) {
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		predecessors := origin.predecessors[[2]string{parent, next}]
		if len(predecessors) == 0 {
			return next, nil
		}
		if len(predecessors) != 1 {
			return "", fmt.Errorf("original saved chain has multiple predecessors")
		}
		predecessor := predecessors[0]
		if seen[predecessor] {
			return "", fmt.Errorf("original saved chain contains a cycle")
		}
		seen[predecessor] = true
		next = predecessor
	}
}
