package history

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type smartlistDeletionAmendment struct {
	entryID       string
	before, after smartlistHistoricalAmendment
}

type smartlistAmendmentPlan struct {
	deletions   []smartlistDeletionAmendment
	withoutUndo []db.SmartlistRecord
}

// Each Before is planned against its own historical present. Future path
// changes and departures cannot become evidence for an earlier Undo image.
func planSmartlistDeletionAmendments(ctx context.Context, lib string, evidence *smartlistEvidence, orders smartlistOrderEvidence, plan *smartlistLinkPlan) (*smartlistAmendmentPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(orders) != len(evidence.versions) {
		return nil, errors.New("historical Smartlist order evidence is incomplete")
	}
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	previous, err := entries(dir)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Entry, len(previous))
	for _, entry := range previous {
		byID[entry.ID] = entry
	}
	target := filepath.Join(lib, "Database2", "m.db")
	amendments := &smartlistAmendmentPlan{}
	for v, version := range evidence.versions {
		owned := plan.deletions[version.entryID]
		if version.after || len(owned) == 0 {
			continue
		}
		entry, exists := byID[version.entryID]
		if !exists || entry.Operation != "playlist.rm" || entry.Before == nil || entry.After == nil || v+1 >= len(evidence.versions) || !evidence.versions[v+1].after || evidence.versions[v+1].entryID != entry.ID {
			return nil, smartlistAmbiguous("original deletion evidence changed before amendment")
		}
		if version.inventory == nil || evidence.versions[v+1].inventory == nil {
			return nil, errors.New("original deletion lacks native database images")
		}
		past := smartlistEvidence{current: version.inventory, hash: entry.Before.Databases[target].Hash, versions: evidence.versions[:v], pathIDs: make(map[string]map[int64]bool)}
		for _, image := range past.versions {
			past.index(image.inventory)
		}
		past.live = past.index(past.current)
		corrected, err := planSmartlistLinks(ctx, &past, orders[:v])
		if err != nil {
			return nil, err
		}
		patches := make(map[string]smartlistPatch, len(corrected.patches))
		for _, patch := range corrected.patches {
			patches[patch.before.UUID] = patch
		}
		before := smartlistHistoricalAmendment{entryID: entry.ID, image: entry.Before.Databases[target]}
		after := smartlistHistoricalAmendment{entryID: entry.ID, image: entry.After.Databases[target]}
		for _, row := range owned {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			original, exists := smartlistSavedRow(version.inventory, row.UUID)
			continuous := exists && sameSmartlistLinkage(original, row)
			for _, image := range evidence.versions[v:] {
				if image.inventory == nil {
					continuous = false
					break
				}
				historical, present := smartlistSavedRow(image.inventory, row.UUID)
				if !present || !sameSmartlistLinkage(historical, original) {
					continuous = false
					break
				}
			}
			if !continuous {
				// An observed disappearance or changed linkage cannot identify a
				// later row as an original-delete casualty. Its current broken
				// state is still removed, but it receives no borrowed Undo entry.
				amendments.withoutUndo = append(amendments.withoutUndo, row)
				continue
			}
			if patch, changed := patches[row.UUID]; changed {
				if patch.after == nil {
					// Historical presence alone does not prove a valid restoration
					// order. Remove the broken current row without borrowing Undo.
					amendments.withoutUndo = append(amendments.withoutUndo, row)
					continue
				}
				before.patches = append(before.patches, patch)
			}
			if remaining, exists := smartlistSavedRow(evidence.versions[v+1].inventory, row.UUID); exists {
				// Identity and retained linkage must match both sides of the actual
				// delete. A later-created UUID cannot borrow its Undo entry.
				if !sameSmartlistLinkage(remaining, original) {
					return nil, smartlistAmbiguous("saved deletion linkage changed across historical images")
				}
				after.patches = append(after.patches, smartlistPatch{before: remaining})
			}
		}
		if len(before.patches) != 0 || len(after.patches) != 0 {
			amendments.deletions = append(amendments.deletions, smartlistDeletionAmendment{entryID: entry.ID, before: before, after: after})
		}
	}
	return amendments, nil
}

func sameSmartlistLinkage(a, b db.SmartlistRecord) bool {
	return a.UUID == b.UUID && a.ParentPath == b.ParentPath && a.NextPath == b.NextPath && a.NextUUID == b.NextUUID
}

func smartlistSavedRow(inventory *db.SmartlistInventory, id string) (db.SmartlistRecord, bool) {
	for _, row := range inventory.Saved {
		if row.UUID == id {
			return row, true
		}
	}
	return db.SmartlistRecord{}, false
}
