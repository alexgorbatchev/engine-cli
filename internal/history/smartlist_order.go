package history

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type smartlistOrdinaryLink struct{ parent, next int64 }

// Order evidence is read separately so the accepted identity resolver remains
// unchanged. Each map corresponds to that resolver's exact historical image.
type smartlistOrderEvidence []map[int64]smartlistOrdinaryLink

func readSmartlistOrderEvidence(ctx context.Context, lib string, evidence *smartlistEvidence) (smartlistOrderEvidence, error) {
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
	path := filepath.Join(lib, "Database2", "m.db")
	orders := make(smartlistOrderEvidence, len(evidence.versions))
	for i, version := range evidence.versions {
		entry, exists := byID[version.entryID]
		if !exists || entry.Operation != version.operation || entry.Before == nil || entry.After == nil {
			return nil, smartlistAmbiguous("history changed before reading native sibling order")
		}
		image := entry.Before
		if version.after {
			image = entry.After
		}
		state := image.Databases[path]
		if version.inventory == nil {
			if state.Path != "" || state.Hash != "" {
				return nil, smartlistAmbiguous("historical database presence changed")
			}
			continue
		}
		inventory, _, err := readSmartlistVersion(ctx, state.Path, state.Hash)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(inventory, version.inventory) {
			return nil, smartlistAmbiguous("native sibling order has a different identity schema")
		}
		orders[i], err = readSmartlistOrdinaryLinks(ctx, state.Path, state.Hash)
		if err != nil {
			return nil, err
		}
	}
	return orders, nil
}

func readSmartlistOrdinaryLinks(ctx context.Context, path, expected string) (map[int64]smartlistOrdinaryLink, error) {
	before, err := fingerprint(ctx, path)
	if err != nil {
		return nil, err
	}
	if expected == "" || before != expected {
		return nil, smartlistAmbiguous("native sibling image does not match history")
	}
	d, err := db.Open(path, true)
	if err != nil {
		return nil, err
	}
	links, readErr := func() (map[int64]smartlistOrdinaryLink, error) {
		rows, err := d.QueryContext(ctx, `SELECT id,parentListId,nextListId FROM Playlist`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		links := make(map[int64]smartlistOrdinaryLink)
		for rows.Next() {
			var id int64
			var link smartlistOrdinaryLink
			if err := rows.Scan(&id, &link.parent, &link.next); err != nil {
				return nil, err
			}
			if _, duplicate := links[id]; duplicate {
				return nil, fmt.Errorf("duplicate native playlist identity")
			}
			links[id] = link
		}
		return links, rows.Err()
	}()
	if err := errors.Join(readErr, d.Close()); err != nil {
		return nil, err
	}
	after, err := fingerprint(ctx, path)
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, smartlistAmbiguous("native sibling image changed while reading")
	}
	return links, nil
}
