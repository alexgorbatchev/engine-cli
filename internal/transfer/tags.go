package transfer

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func readTags(ctx context.Context, c *collection) error {
	s, err := tags.Read(ctx, c.Path)
	if err != nil {
		return err
	}
	c.Tags = s
	byOrigin := map[tags.TrackOrigin][]string{}
	for _, a := range s.Assignments {
		origin := tags.TrackOrigin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		byOrigin[origin] = append(byOrigin[origin], a.ValueID)
	}
	for _, category := range s.Categories {
		key := "tag_category:" + category.ID
		c.Entities[key] = &entity{Kind: "tag_category", Key: key, Values: row{"name": category.Name}}
		for _, value := range category.Values {
			key := "tag_value:" + value.ID
			c.Entities[key] = &entity{Kind: "tag_value", Key: key, Values: row{"name": value.Name, "category": category.ID}}
		}
	}
	for _, track := range slices.Collect(entityTracks(c)) {
		origin := tags.TrackOrigin{UUID: str(track.Values["originDatabaseUuid"]), ID: number(track.Values["originTrackId"])}
		values := append([]string{}, byOrigin[origin]...)
		delete(byOrigin, origin)
		key := "tag_assignment:" + track.Key
		c.Entities[key] = &entity{Kind: "tag_assignment", Key: key, Values: row{"track": track.Key, "values": values}}
	}
	if len(byOrigin) > 0 {
		return &progress.Error{Code: "tag_assignments_stale", Cause: fmt.Errorf("custom-tag assignments reference unavailable tracks; review tag reconcile --dry-run and apply its exact plan before syncing")}
	}
	return nil
}

func entityTracks(c *collection) func(func(*entity) bool) {
	return func(yield func(*entity) bool) {
		for _, e := range c.Entities {
			if e.Kind == "track" && !yield(e) {
				return
			}
		}
	}
}

func selectTags(c *collection, selected map[string]bool) {
	for key, e := range c.Entities {
		if e.Kind != "tag_assignment" || !selected[str(e.Values["track"])] {
			continue
		}
		selected[key] = true
		for _, id := range tagValues(e) {
			value := c.Entities["tag_value:"+id]
			if value != nil {
				selected[value.Key] = true
				selected["tag_category:"+str(value.Values["category"])] = true
			}
		}
	}
}

func tagValues(e *entity) []string {
	values, _ := e.Values["values"].([]string)
	return values
}

func isTag(e *entity) bool { return strings.HasPrefix(e.Kind, "tag_") }

// plannedTags applies only accepted entity decisions. Destination-only taxonomy
// remains unless root mirroring explicitly removes it; playlist mirrors never
// remove definitions used outside their selected tracks.
func (p *plan) plannedTagEntities() map[string]*entity {
	entities := map[string]*entity{}
	for key, e := range p.destination.Entities {
		if !p.remove[key] {
			entities[key] = e
		}
	}
	for key, e := range p.desired {
		entities[key] = e
	}
	return entities
}

func (p *plan) plannedTags() tags.State {
	entities := p.plannedTagEntities()
	state := tags.State{Categories: []tags.Category{}, Assignments: []tags.Assignment{}}
	valuesByCategory := map[string][]tags.Value{}
	for _, key := range orderedKeys(entities) {
		e := entities[key]
		if e.Kind == "tag_value" {
			category := str(e.Values["category"])
			valuesByCategory[category] = append(valuesByCategory[category], tags.Value{ID: strings.TrimPrefix(key, "tag_value:"), Name: str(e.Values["name"])})
		}
	}
	for _, key := range orderedKeys(entities) {
		e := entities[key]
		if e.Kind != "tag_category" {
			continue
		}
		c := tags.Category{ID: strings.TrimPrefix(key, "tag_category:"), Name: str(e.Values["name"]), Values: []tags.Value{}}
		c.Values = append(c.Values, valuesByCategory[c.ID]...)
		state.Categories = append(state.Categories, c)
	}
	for _, key := range orderedKeys(entities) {
		e := entities[key]
		if e.Kind != "tag_assignment" {
			continue
		}
		track := entities[str(e.Values["track"])]
		if track == nil {
			continue
		}
		for _, id := range tagValues(e) {
			if entities["tag_value:"+id] != nil {
				state.Assignments = append(state.Assignments, tags.Assignment{OriginUUID: str(track.Values["originDatabaseUuid"]), OriginTrackID: number(track.Values["originTrackId"]), ValueID: id})
			}
		}
	}
	// Origin ordering can differ from the transfer's remapped track keys.
	slices.SortFunc(state.Assignments, func(a, b tags.Assignment) int {
		if n := strings.Compare(a.OriginUUID, b.OriginUUID); n != 0 {
			return n
		}
		if a.OriginTrackID < b.OriginTrackID {
			return -1
		}
		if a.OriginTrackID > b.OriginTrackID {
			return 1
		}
		return strings.Compare(a.ValueID, b.ValueID)
	})
	counts := map[string]int{}
	for _, a := range state.Assignments {
		counts[a.ValueID]++
	}
	for i := range state.Categories {
		for j := range state.Categories[i].Values {
			v := &state.Categories[i].Values[j]
			v.TrackCount = counts[v.ID]
		}
	}
	return state
}

func (p *plan) checkTagReferences() {
	entities := p.plannedTagEntities()
	for key, e := range entities {
		if e.Kind == "tag_value" && entities["tag_category:"+str(e.Values["category"])] == nil {
			p.conflict(Change{Entity: e.Kind, ID: key}, "sync_tag_reference_conflict", "retained values require a category removed by this plan")
		}
		if e.Kind == "tag_assignment" && entities[str(e.Values["track"])] != nil {
			for _, id := range tagValues(e) {
				if entities["tag_value:"+id] == nil {
					p.conflict(Change{Entity: e.Kind, ID: key}, "sync_tag_reference_conflict", "retained track assignments require a value removed by this plan")
				}
			}
		}
	}
}

func (p *plan) checkTagNames() {
	state := p.plannedTags()
	names := map[string]string{}
	for _, c := range state.Categories {
		key := "category:" + cases.Fold().String(norm.NFC.String(c.Name))
		if previous := names[key]; previous != "" && previous != c.ID {
			p.conflict(Change{Entity: "tag_category", ID: c.ID, Label: c.Name}, "sync_tag_name_conflict", "distinct category identities use the same normalized name; resolve their taxonomy before syncing")
		}
		names[key] = c.ID
		for _, v := range c.Values {
			key := fmt.Sprintf("value:%s:%s", c.ID, cases.Fold().String(norm.NFC.String(v.Name)))
			if previous := names[key]; previous != "" && previous != v.ID {
				p.conflict(Change{Entity: "tag_value", ID: v.ID, Label: v.Name}, "sync_tag_name_conflict", "distinct value identities use the same name within a category; resolve their taxonomy before syncing")
			}
			names[key] = v.ID
		}
	}
}
