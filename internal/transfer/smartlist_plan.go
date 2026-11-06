package transfer

import (
	"context"
	"fmt"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func (p *plan) planSmartlists(ctx context.Context, o Options) error {
	p.finalEntities = map[string]*entity{}
	for key, e := range p.destination.Entities {
		if !p.remove[key] {
			p.finalEntities[key] = e
		}
	}
	for key, e := range p.desired {
		p.finalEntities[key] = e
	}
	p.planPlaylistOrders()
	p.sourceSmartlistActions(o)
	p.destinationSmartlistActions()
	paths, err := plannedPlaylistPaths(ctx, p.finalEntities)
	if err != nil {
		p.conflict(Change{Entity: "playlist"}, "sync_playlist_graph_invalid", err.Error())
		return ctx.Err()
	}
	p.expectedSmartlists = map[string]*entity{}
	sourceLinks, destinationLinks := savedTopologyFrom(p.source), savedTopologyFrom(p.destination)
	for _, key := range orderedKeys(p.finalEntities) {
		original := p.finalEntities[key]
		if original.Kind != "smartlist" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		origin := destinationLinks
		if p.desired[key] != nil {
			origin = sourceLinks
		}
		next, err := p.smartlistSuccessor(ctx, origin, original)
		if err != nil {
			p.conflict(savedChange(original), "sync_smartlist_link_unresolved", err.Error())
			continue
		}
		parent, exists := paths[original.Parent]
		if !exists {
			p.conflict(savedChange(original), "sync_smartlist_parent_removed", "retained Smartlist parent is absent from the final destination")
			continue
		}
		updated := *original
		updated.Values, updated.Next = clone(original.Values), next
		setSavedLink(updated.Values, "parentPlaylistPath", parent)
		nextPath, nextUUID := "", ""
		if target := p.finalEntities[next]; target != nil {
			if target.Kind == "playlist" {
				nextPath = paths[next]
			} else {
				nextUUID = str(target.Values["listUuid"])
			}
		}
		setSavedLink(updated.Values, "nextPlaylistPath", nextPath)
		setSavedLink(updated.Values, "nextListUuid", nextUUID)
		p.expectedSmartlists[key] = &updated
		p.finalEntities[key] = &updated
		if !reflect.DeepEqual(updated.Values, original.Values) || updated.Next != original.Next {
			p.recordSmartlistReconnect(&updated)
		}
	}
	inventory := plannedSmartlistInventory(p.finalEntities, paths)
	issues, _, err := db.ValidateSmartlistInventory(ctx, inventory)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		p.conflict(Change{Entity: "smartlist", ID: "smartlist:" + issue.UUID, Label: issue.Name}, "sync_smartlist_graph_invalid", fmt.Sprintf("final destination Smartlist links are invalid: %s", issue.Kind))
	}
	return p.checkSmartlistConstraints(ctx)
}

func savedChange(e *entity) Change {
	return Change{Entity: "smartlist", ID: e.Key, Label: str(e.Values["title"])}
}

// Native NULL and empty text describe the same empty edge but are not the same
// stored cell. Leave every unchanged cell exactly as observed.
func setSavedLink(values row, column, value string) {
	if str(values[column]) != value {
		values[column] = value
	}
}

func (p *plan) recordSmartlistReconnect(updated *entity) {
	change := savedChange(updated)
	change.Action = "update"
	change.Code = "sync_smartlist_reconnected"
	change.Reason = "reconnect saved links to the final destination playlist identities"
	if p.desired[updated.Key] == nil {
		p.report.Changes = append(p.report.Changes, change)
		p.count(change)
	} else {
		for index := range p.report.Changes {
			if p.report.Changes[index].ID == updated.Key && (p.report.Changes[index].Action == "add" || p.report.Changes[index].Action == "update") {
				p.report.Changes[index].Code = change.Code
				p.report.Changes[index].Reason = change.Reason
			}
		}
	}
	p.desired[updated.Key] = updated
}

func (p *plan) sourceSmartlistActions(o Options) {
	if !o.Scope.Root || p.source.Smartlists == nil {
		return
	}
	for _, action := range p.source.Smartlists.Actions {
		change := Change{Entity: "smartlist", ID: "smartlist:" + action.UUID, Label: action.Name}
		if action.Action == "omit" {
			change.Action, change.Code = "omit", "sync_source_smartlist_omitted"
			change.Reason = "omit the unresolved source Smartlist without modifying the source"
			p.report.Summary.SourceSmartlistsOmitted++
		} else {
			change.Action, change.Code = "reconnect", "sync_source_smartlist_reconnected"
			change.Reason = "resolve source Smartlist links from recorded native history without modifying the source"
			p.report.Summary.SourceSmartlistsReconnected++
		}
		p.report.Changes = append(p.report.Changes, change)
	}
}

func (p *plan) destinationSmartlistActions() {
	if p.destination.Smartlists == nil {
		return
	}
	for _, action := range p.destination.Smartlists.Actions {
		change := Change{Entity: "smartlist", ID: "smartlist:" + action.UUID, Label: action.Name}
		if action.Action == "omit" {
			change.Action, change.Code = "prepare-remove", "sync_destination_smartlist_removed"
			change.Reason = "remove the unresolved Smartlist during protected destination preparation"
			p.report.Summary.DestinationSmartlistsRemoved++
		} else {
			change.Action, change.Code = "prepare-reconnect", "sync_destination_smartlist_reconnected"
			change.Reason = "reconnect saved links during protected destination preparation"
			p.report.Summary.DestinationSmartlistsReconnected++
		}
		p.report.Changes = append(p.report.Changes, change)
	}
}
