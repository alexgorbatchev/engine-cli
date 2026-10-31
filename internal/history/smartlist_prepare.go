package history

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type smartlistPrepared struct {
	dir        string
	journal    smartlistJournal
	undoID     string
	references map[string]string
}

// All referenced images have stable names outside the active transaction. A
// later archival rename of active repair evidence cannot break selective Undo.
func prepareSmartlistRepair(ctx context.Context, lib, historyDir string, plan *smartlistRepairPlan) (*smartlistPrepared, error) {
	var random [entryIDBytes]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(random[:])
	root := filepath.Dir(historyDir)
	if err := createSmartlistDirectory(filepath.Join(root, "smartlist-repairs")); err != nil {
		return nil, err
	}
	images := filepath.Join(root, "smartlist-repairs", id+"-images")
	if err := createSmartlistDirectory(images); err != nil {
		return nil, err
	}
	active := filepath.Join(root, "smartlist-repair")
	if _, err := os.Lstat(active); err == nil {
		if err := checkSmartlistRepair(historyDir); err != nil {
			return nil, err
		}
		if err := publishEntry(active, filepath.Join(root, "smartlist-repairs", id+"-previous")); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := createSmartlistDirectory(active); err != nil {
		return nil, err
	}
	p := &smartlistPrepared{dir: active, journal: smartlistJournal{Version: 1, Library: plan.evidence.current.EngineUUID}}
	if err := p.stageEntries(ctx, lib, historyDir, images, id, plan); err != nil {
		return nil, err
	}
	source := filepath.Join(lib, "Database2", "m.db")
	original := filepath.Join(active, "original.db")
	if err := copyDatabase(ctx, source, original); err != nil {
		return nil, err
	}
	if actual, err := fingerprint(ctx, original); err != nil || actual != plan.evidence.hash {
		return nil, errors.Join(errors.New("Smartlist source changed during preparation"), err)
	}
	staged := filepath.Join(active, "repaired.db")
	hash, err := stageSmartlistImage(ctx, original, staged, plan.evidence.hash, plan.links.patches)
	if err != nil {
		return nil, err
	}
	p.journal.Images = append(p.journal.Images, smartlistJournalImage{Target: filepath.Join("Database2", "m.db"), Database: true, Original: "original.db", OriginalHash: plan.evidence.hash, Staged: "repaired.db", StagedHash: hash})
	return p, nil
}

func (p *smartlistPrepared) stageEntries(ctx context.Context, lib, historyDir, images, id string, plan *smartlistRepairPlan) error {
	previous, err := entries(historyDir)
	if err != nil {
		return err
	}
	byID := make(map[string]Entry, len(previous))
	for _, entry := range previous {
		byID[entry.ID] = entry
	}
	target := filepath.Join(lib, "Database2", "m.db")
	for _, amendment := range plan.amendments.deletions {
		entry, exists := byID[amendment.entryID]
		if !exists {
			return smartlistAmbiguous("original deletion disappeared during staging")
		}
		if err := p.referenceEntry(lib, &entry); err != nil {
			return err
		}
		for _, side := range []struct {
			name string
			plan smartlistHistoricalAmendment
		}{{"before", amendment.before}, {"after", amendment.after}} {
			if len(side.plan.patches) == 0 {
				continue
			}
			path := filepath.Join(images, entry.ID+"-"+side.name+".db")
			hash, err := stageHistoricalSmartlistImage(ctx, path, side.plan)
			if err != nil {
				return err
			}
			image := entry.Before
			if side.name == "after" {
				image = entry.After
			}
			copy := *image
			copy.Databases = maps.Clone(image.Databases)
			copy.Databases[target] = databaseState{Path: path, Hash: hash}
			if side.name == "before" {
				entry.Before = &copy
			} else {
				entry.After = &copy
			}
		}
		if err := p.stageEntry(ctx, historyDir, &entry, false); err != nil {
			return err
		}
		if err := p.referenceEntry(lib, &entry); err != nil {
			return err
		}
	}
	if plan.review.Reconnected == 0 {
		return nil
	}
	entry, err := stageSmartlistReconnectEntry(ctx, lib, images, id, previous, plan)
	if err != nil {
		return err
	}
	p.undoID = entry.ID
	if err := p.referenceEntry(lib, entry); err != nil {
		return err
	}
	return p.stageEntry(ctx, historyDir, entry, true)
}

func (p *smartlistPrepared) stageEntry(ctx context.Context, historyDir string, entry *Entry, created bool) error {
	entities, err := changedEntities(ctx, entry)
	if err != nil {
		return err
	}
	entry.Entities = entities
	image := smartlistJournalImage{Target: filepath.Join(".engine-cli", "history", entry.ID+".json"), Staged: entry.ID + ".json"}
	if !created {
		image.Original = entry.ID + "-original.json"
		data, err := os.ReadFile(filepath.Join(historyDir, entry.ID+".json"))
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(p.dir, image.Original), data); err != nil {
			return err
		}
		image.OriginalHash = hashText(string(data))
	}
	if err := writeEntry(p.dir, entry); err != nil {
		return err
	}
	image.StagedHash, err = smartlistJournalHash(ctx, filepath.Join(p.dir, image.Staged), false)
	if err != nil {
		return err
	}
	p.journal.Images = append(p.journal.Images, image)
	return nil
}

func stageSmartlistReconnectEntry(ctx context.Context, lib, images, id string, previous []Entry, plan *smartlistRepairPlan) (*Entry, error) {
	target := filepath.Join(lib, "Database2", "m.db")
	baseline := databaseState{Path: filepath.Join(images, "deletion-baseline.db"), Hash: plan.evidence.hash}
	var removed []smartlistPatch
	for _, patch := range plan.links.patches {
		if patch.after == nil {
			removed = append(removed, patch)
		}
	}
	if len(removed) == 0 {
		if err := copyDatabase(ctx, target, baseline.Path); err != nil {
			return nil, err
		}
		if hash, err := fingerprint(ctx, baseline.Path); err != nil || hash != baseline.Hash {
			return nil, errors.Join(errors.New("reconnect baseline changed"), err)
		}
	} else {
		hash, err := stageSmartlistRows(ctx, target, baseline.Path, baseline.Hash, removed, func(before, after *db.SmartlistInventory) error {
			return validateHistoricalSmartlistStage(before, after, removed)
		})
		if err != nil {
			return nil, err
		}
		baseline.Hash = hash
	}
	after := databaseState{Path: filepath.Join(images, "reconnected.db")}
	hash, err := stageSmartlistImage(ctx, target, after.Path, plan.evidence.hash, plan.links.patches)
	if err != nil {
		return nil, err
	}
	after.Hash = hash
	libraryID := id
	if len(previous) != 0 {
		libraryID = previous[0].LibraryID
	}
	return &Entry{ID: id, Operation: "library.smartlist-repair.apply", Scope: ScopeCollection, Created: time.Now().UTC(), Library: lib, LibraryID: libraryID, State: "complete", Before: &snapshot{Databases: map[string]databaseState{target: baseline}}, After: &snapshot{Databases: map[string]databaseState{target: after}}}, nil
}
