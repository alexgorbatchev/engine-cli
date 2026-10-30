package db

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type playlistRow struct {
	Playlist
	next sql.NullInt64
}

// Querier reads from a database connection or an open transaction.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Executor supports the same statements on a connection and inside an atomic write.
type Executor interface {
	Querier
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func playlistRows(ctx context.Context, d Querier) ([]playlistRow, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,title,COALESCE(parentListId,0),nextListId FROM Playlist ORDER BY parentListId,id`)
	if err != nil {
		return nil, fmt.Errorf("querying playlists: %w", err)
	}
	defer rows.Close()
	var result []playlistRow
	for rows.Next() {
		var p playlistRow
		if err := rows.Scan(&p.ID, &p.Title, &p.ParentListID, &p.next); err != nil {
			return nil, fmt.Errorf("scanning playlist: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// Native sibling order is a linked list, not insertion order or alphabetical order.
// Unlinked rows (including NULL tails) retain their deterministic database order.
func siblingOrder(rows []playlistRow, parent int64) ([]int64, error) {
	byID := make(map[int64]playlistRow)
	referenced := make(map[int64]bool)
	for _, p := range rows {
		if p.ParentListID == parent {
			byID[p.ID] = p
			if p.next.Valid && p.next.Int64 != 0 {
				referenced[p.next.Int64] = true
			}
		}
	}
	var ordered []int64
	seen := make(map[int64]bool)
	for _, p := range rows {
		if p.ParentListID != parent || referenced[p.ID] {
			continue
		}
		id := p.ID
		for id != 0 {
			next, exists := byID[id]
			if !exists || seen[id] {
				break
			}
			seen[id] = true
			ordered = append(ordered, id)
			id = next.next.Int64
		}
	}
	if len(ordered) != len(byID) {
		return nil, &progress.Error{Code: "playlist_order_invalid", Cause: fmt.Errorf("cyclic sibling order under playlist %d", parent)}
	}
	return ordered, nil
}

// MovePlaylist atomically repairs the source chain and inserts before a destination sibling.
// A zero beforeID appends to the destination. Playlist identity and memberships never change.
func MovePlaylist(ctx context.Context, database *sql.DB, id, newParentID, beforeID int64) error {
	if database == nil {
		return fmt.Errorf("database cannot be nil")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := playlistRows(ctx, tx)
	if err != nil {
		return err
	}
	byID := make(map[int64]playlistRow, len(rows))
	for _, p := range rows {
		byID[p.ID] = p
	}
	invalid := func(message string) error {
		return &progress.Error{Code: "playlist_move_invalid", Cause: fmt.Errorf("%s", message)}
	}
	source, exists := byID[id]
	if !exists {
		return invalid("playlist no longer exists")
	}
	if newParentID < 0 || beforeID < 0 || beforeID == id {
		return invalid("invalid playlist position")
	}
	seen := make(map[int64]bool)
	for parent := newParentID; parent != 0; {
		p, exists := byID[parent]
		if !exists {
			return invalid("destination parent no longer exists")
		}
		if parent == id || seen[parent] {
			return invalid("destination would create a circular hierarchy")
		}
		seen[parent] = true
		parent = p.ParentListID
	}
	if beforeID != 0 {
		sibling, exists := byID[beforeID]
		if !exists || sibling.ParentListID != newParentID {
			return invalid("insertion sibling is not in the destination folder")
		}
	}
	parents := []int64{source.ParentListID}
	if source.ParentListID != newParentID {
		parents = append(parents, newParentID)
	}
	orders := make(map[int64][]int64, len(parents))
	for _, parent := range parents {
		order, err := siblingOrder(rows, parent)
		if err != nil {
			return err
		}
		orders[parent] = slices.DeleteFunc(order, func(value int64) bool { return value == id })
	}
	index := len(orders[newParentID])
	if beforeID != 0 {
		index = slices.Index(orders[newParentID], beforeID)
	}
	orders[newParentID] = slices.Insert(orders[newParentID], index, id)
	original, err := siblingOrder(rows, source.ParentListID)
	if err != nil {
		return err
	}
	if source.ParentListID == newParentID && slices.Equal(original, orders[newParentID]) {
		return tx.Commit()
	}
	paths, err := playlistPaths(rows)
	if err != nil {
		return err
	}
	if err := unlinkSmartlistPredecessor(ctx, tx, source, paths); err != nil {
		return err
	}
	// Inserting before an ordinary sibling follows the Smartlist chain that
	// already preceded it, as with Engine's native playlist insertion.
	if _, err := tx.ExecContext(ctx, `UPDATE Smartlist SET nextPlaylistPath=? WHERE parentPlaylistPath=? AND nextPlaylistPath=? AND nextListUuid=''`, paths[newParentID]+source.Title+";", paths[newParentID], paths[beforeID]); err != nil {
		return err
	}
	for i := range rows {
		if rows[i].ID == id {
			rows[i].ParentListID = newParentID
		}
	}
	after, err := playlistPaths(rows)
	if err != nil {
		return err
	}
	if err := rewriteSmartlistPaths(ctx, tx, paths, after); err != nil {
		return err
	}
	// NULL releases each unique (parentListId,nextListId) slot before relinking.
	for _, parent := range parents {
		if _, err := tx.ExecContext(ctx, "UPDATE Playlist SET nextListId=NULL WHERE parentListId=?", parent); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE Playlist SET parentListId=?,lastEditTime=CURRENT_TIMESTAMP WHERE id=?", newParentID, id); err != nil {
		return err
	}
	for _, parent := range parents {
		order := orders[parent]
		for i, current := range order {
			var next int64
			if i+1 < len(order) {
				next = order[i+1]
			}
			if _, err := tx.ExecContext(ctx, "UPDATE Playlist SET nextListId=? WHERE id=?", next, current); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
