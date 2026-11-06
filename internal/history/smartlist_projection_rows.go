package history

import (
	"fmt"
	"sort"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func projectSmartlistRows(inventory *db.SmartlistInventory, links *smartlistLinkPlan, projection *SmartlistProjection, original map[string][]any) (*SmartlistProjection, error) {
	patches := make(map[string]smartlistPatch, len(links.patches))
	for _, patch := range links.patches {
		patches[patch.before.UUID] = patch
		action := "omit"
		if patch.after != nil {
			action = "reconnect"
		}
		projection.Actions = append(projection.Actions, SmartlistRepairRow{UUID: patch.before.UUID, Name: patch.before.Name, Action: action})
	}
	sort.Slice(projection.Actions, func(i, j int) bool { return projection.Actions[i].UUID < projection.Actions[j].UUID })
	paths := map[string]int64{"": 0}
	ambiguous := make(map[string]bool)
	for _, playlist := range inventory.Playlists {
		if _, exists := paths[playlist.Path]; exists {
			ambiguous[playlist.Path] = true
		}
		paths[playlist.Path] = playlist.ID
	}
	for _, row := range inventory.Saved {
		before := row
		if patch, exists := patches[row.UUID]; exists {
			if patch.after == nil {
				continue
			}
			row = *patch.after
		}
		parent, parentOK := paths[row.ParentPath]
		next, nextOK := paths[row.NextPath]
		if !parentOK || !nextOK || ambiguous[row.ParentPath] || ambiguous[row.NextPath] {
			return nil, smartlistAmbiguous("projected Smartlist links have no unique native playlist identity")
		}
		values, exists := original[row.UUID]
		if !exists || len(values) != len(projection.Columns) {
			return nil, fmt.Errorf("projected Smartlist has no complete original row")
		}
		projected := SmartlistProjectionRow{Values: make(map[string]any, len(values)), ParentPlaylistID: parent, NextPlaylistID: next}
		for i, column := range projection.Columns {
			value := values[i]
			switch column {
			case "parentPlaylistPath":
				if row.ParentPath != before.ParentPath {
					value = row.ParentPath
				}
			case "nextPlaylistPath":
				if row.NextPath != before.NextPath {
					value = row.NextPath
				}
			case "nextListUuid":
				if row.NextUUID != before.NextUUID {
					value = row.NextUUID
				}
			}
			projected.Values[column] = value
		}
		projection.Rows = append(projection.Rows, projected)
	}
	return projection, nil
}
