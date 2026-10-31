package db

import (
	"context"
	"database/sql"
	"fmt"
)

// SmartlistIssue identifies stored path or sibling-link damage without changing it.
// Paths and rules remain internal; callers can display the affected saved name.
type SmartlistIssue struct {
	UUID string
	Name string
	Kind string
}

type savedSmartlist struct {
	id, name, parent, next, nextID string
}

// SmartlistInventory binds native saved paths to playlist identities in one
// read-only transaction. It is evidence for repair planning, not write consent.
type SmartlistInventory struct {
	EngineUUID     string
	PlaylistSchema string
	Playlists      []SmartlistPlaylist
	Saved          []SmartlistRecord
	Issues         []SmartlistIssue
	Causes         []SmartlistCause
}

type SmartlistPlaylist struct {
	ID, ParentID int64
	Path         string
}

type SmartlistRecord struct {
	UUID, Name, ParentPath, NextPath, NextUUID string
}

// InspectSmartlists reads one consistent native snapshot. Older native libraries
// genuinely lacking Smartlist are healthy; malformed schema objects fail closed.
func InspectSmartlists(ctx context.Context, database *sql.DB) ([]SmartlistIssue, error) {
	inventory, err := ReadSmartlistInventory(ctx, database)
	if err != nil {
		return nil, err
	}
	return inventory.Issues, nil
}

// ReadSmartlistInventory shares the detector's schema, identity and graph
// checks with history-backed planning; it never enrolls or repairs storage.
func ReadSmartlistInventory(ctx context.Context, database *sql.DB) (*SmartlistInventory, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	exists, err := hasSmartlistTable(ctx, tx)
	if err != nil {
		return nil, err
	}
	var inventory SmartlistInventory
	if err := tx.QueryRowContext(ctx, `SELECT uuid FROM Information WHERE id=1`).Scan(&inventory.EngineUUID); err != nil {
		return nil, err
	}
	if inventory.EngineUUID == "" {
		return nil, fmt.Errorf("library identity is empty")
	}
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='Playlist'`).Scan(&inventory.PlaylistSchema); err != nil {
		return nil, err
	}
	playlists, err := playlistRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	paths, err := playlistPaths(playlists)
	if err != nil {
		return nil, err
	}
	for _, playlist := range playlists {
		inventory.Playlists = append(inventory.Playlists, SmartlistPlaylist{ID: playlist.ID, ParentID: playlist.ParentListID, Path: paths[playlist.ID]})
	}
	if !exists {
		return &inventory, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT listUuid,COALESCE(title,''),COALESCE(parentPlaylistPath,''),COALESCE(nextPlaylistPath,''),COALESCE(nextListUuid,'') FROM Smartlist ORDER BY listUuid`)
	if err != nil {
		return nil, err
	}
	var saved []savedSmartlist
	identities := make(map[string]bool)
	for rows.Next() {
		var value savedSmartlist
		if err := rows.Scan(&value.id, &value.name, &value.parent, &value.next, &value.nextID); err != nil {
			rows.Close()
			return nil, err
		}
		if identities[value.id] {
			rows.Close()
			return nil, fmt.Errorf("Smartlist contains duplicate identities")
		}
		identities[value.id] = true
		saved = append(saved, value)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	inventory.Issues, inventory.Causes, err = inspectSmartlistLinks(ctx, playlists, paths, saved)
	if err != nil {
		return nil, err
	}
	for _, row := range saved {
		inventory.Saved = append(inventory.Saved, SmartlistRecord{UUID: row.id, Name: row.name, ParentPath: row.parent, NextPath: row.next, NextUUID: row.nextID})
	}
	return &inventory, nil
}
