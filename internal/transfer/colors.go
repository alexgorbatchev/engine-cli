package transfer

import (
	"context"
	"fmt"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func readColors(ctx context.Context, c *collection) error {
	state, err := colors.Read(ctx, c.Path)
	if err != nil {
		return err
	}
	c.Colors = state
	byOrigin := map[companion.Origin]string{}
	for _, assignment := range state.Assignments {
		byOrigin[companion.Origin{UUID: assignment.OriginUUID, ID: assignment.OriginTrackID}] = assignment.Color
	}
	for _, track := range slices.Collect(entityTracks(c)) {
		origin := companion.Origin{UUID: str(track.Values["originDatabaseUuid"]), ID: number(track.Values["originTrackId"])}
		key := "color_assignment:" + track.Key
		c.Entities[key] = &entity{Kind: "color_assignment", Key: key, Values: row{"track": track.Key, "color": byOrigin[origin]}}
		delete(byOrigin, origin)
	}
	if len(byOrigin) != 0 {
		return &progress.Error{Code: "color_assignments_stale", Cause: fmt.Errorf("track colors reference unavailable tracks; review and apply color reconciliation before syncing")}
	}
	return nil
}

func selectColors(c *collection, selected map[string]bool) {
	for key, entity := range c.Entities {
		if entity.Kind == "color_assignment" && selected[str(entity.Values["track"])] {
			selected[key] = true
		}
	}
}

func emptyAssignment(e *entity) bool {
	return e.Kind == "tag_assignment" && len(tagValues(e)) == 0 || e.Kind == "color_assignment" && str(e.Values["color"]) == ""
}

func (p *plan) plannedColors() colors.State {
	entities := p.plannedEntities()
	state := colors.State{Assignments: []colors.Assignment{}}
	for _, key := range orderedKeys(entities) {
		entity := entities[key]
		if entity.Kind != "color_assignment" || str(entity.Values["color"]) == "" {
			continue
		}
		track := entities[str(entity.Values["track"])]
		if track != nil {
			state.Assignments = append(state.Assignments, colors.Assignment{OriginUUID: str(track.Values["originDatabaseUuid"]), OriginTrackID: number(track.Values["originTrackId"]), Color: str(entity.Values["color"])})
		}
	}
	return state
}
