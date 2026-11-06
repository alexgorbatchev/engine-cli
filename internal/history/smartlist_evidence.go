package history

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type smartlistVersion struct {
	entryID, operation string
	after              bool
	inventory          *db.SmartlistInventory
	paths              map[int64]string
}

type smartlistEvidence struct {
	current  *db.SmartlistInventory
	hash     string
	versions []smartlistVersion
	pathIDs  map[string]map[int64]bool
	live     map[int64]string
}

type smartlistPathIdentity struct {
	id          int64
	currentPath string
	present     bool
	deleteEntry string
}

// This reader deliberately acquires no history lock and enrolls no storage.
// A future writer must revalidate this evidence under the protected write lock.
func readSmartlistEvidence(ctx context.Context, lib string) (*smartlistEvidence, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(dir)), "Database2", "m.db")
	current, hash, err := readSmartlistVersion(ctx, path, "")
	if err != nil {
		return nil, err
	}
	evidence := &smartlistEvidence{current: current, hash: hash, pathIDs: make(map[string]map[int64]bool)}
	evidence.live = evidence.index(current)
	if len(current.Issues) == 0 {
		return evidence, nil
	}
	previous, err := entries(dir)
	if err != nil {
		return nil, err
	}
	// entries is newest-first; identity disappearance must be evaluated in
	// chronological order, including both sides of each native mutation.
	for i := len(previous) - 1; i >= 0; i-- {
		entry := previous[i]
		if entry.After == nil {
			return nil, &progress.Error{Code: "history_recovery_required", Cause: fmt.Errorf("operation %s is incomplete", entry.ID)}
		}
		if entry.Scope != ScopeCollection {
			continue
		}
		if i > 0 && entry.Created.Equal(previous[i-1].Created) {
			return nil, smartlistAmbiguous("history chronology is ambiguous")
		}
		for _, side := range []struct {
			snapshot *snapshot
			after    bool
		}{{entry.Before, false}, {entry.After, true}} {
			state := side.snapshot.Databases[path]
			if (state.Hash == "") != (state.Path == "") {
				return nil, fmt.Errorf("Smartlist history %s contains incomplete database evidence", entry.ID)
			}
			version := smartlistVersion{entryID: entry.ID, operation: entry.Operation, after: side.after}
			if state.Hash != "" {
				version.inventory, _, err = readSmartlistVersion(ctx, state.Path, state.Hash)
				if err != nil {
					return nil, fmt.Errorf("validating Smartlist history %s: %w", entry.ID, err)
				}
				if version.inventory.EngineUUID != current.EngineUUID {
					return nil, smartlistAmbiguous("history belongs to a different Engine library identity")
				}
				// SQLite owns the CREATE text and updates it for ALTER TABLE.
				// A matching content hash per snapshot does not prove that its
				// native identity constraints survived between generations.
				if version.inventory.PlaylistSchema != current.PlaylistSchema {
					return nil, smartlistAmbiguous("native playlist identity schema changed across history")
				}
			}
			version.paths = evidence.index(version.inventory)
			evidence.versions = append(evidence.versions, version)
		}
	}
	return evidence, nil
}

func (e *smartlistEvidence) index(inventory *db.SmartlistInventory) map[int64]string {
	paths := make(map[int64]string)
	if inventory == nil {
		return paths
	}
	for _, playlist := range inventory.Playlists {
		paths[playlist.ID] = playlist.Path
		if e.pathIDs[playlist.Path] == nil {
			e.pathIDs[playlist.Path] = make(map[int64]bool)
		}
		e.pathIDs[playlist.Path][playlist.ID] = true
	}
	return paths
}

func readSmartlistVersion(ctx context.Context, path, expected string) (*db.SmartlistInventory, string, error) {
	before, err := fingerprint(ctx, path)
	if err != nil {
		return nil, "", err
	}
	if expected != "" && before != expected {
		return nil, "", fmt.Errorf("saved Smartlist database fingerprint does not match history")
	}
	d, err := db.Open(path, true)
	if err != nil {
		return nil, "", err
	}
	inventory, readErr := db.ReadSmartlistInventory(ctx, d)
	if err := errors.Join(readErr, d.Close()); err != nil {
		return nil, "", err
	}
	after, err := fingerprint(ctx, path)
	if err != nil {
		return nil, "", err
	}
	if after != before {
		return nil, "", smartlistAmbiguous("database changed while reading Smartlist evidence")
	}
	return inventory, before, nil
}

func (e *smartlistEvidence) resolvePath(ctx context.Context, path string) (*smartlistPathIdentity, error) {
	if path == "" {
		return &smartlistPathIdentity{present: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := e.pathIDs[path]
	if len(ids) > 1 {
		return nil, smartlistAmbiguous("stored path identifies multiple native playlists")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var result smartlistPathIdentity
	for id := range ids {
		result.id = id
	}
	seen, disappeared := false, false
	for i := 0; i <= len(e.versions); i++ {
		version := smartlistVersion{paths: e.live}
		if i < len(e.versions) {
			version = e.versions[i]
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, present := version.paths[result.id]
		if present && disappeared {
			return nil, smartlistAmbiguous("native playlist identity reappears after removal")
		}
		if seen && !present && !disappeared {
			disappeared = true
			if version.after && version.operation == "playlist.rm" {
				result.deleteEntry = version.entryID
			}
		}
		seen = seen || present
	}
	result.currentPath, result.present = e.live[result.id]
	return &result, nil
}

func smartlistAmbiguous(message string) error {
	return &progress.Error{Code: "smartlist_repair_ambiguous", Cause: errors.New(message)}
}
