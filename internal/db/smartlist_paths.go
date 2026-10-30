package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// Engine stores Smartlist links as semicolon-terminated PlaylistPath values,
// not playlist IDs. Keep the native title bytes and separator convention.
func playlistPaths(rows []playlistRow) (map[int64]string, error) {
	byID := make(map[int64]playlistRow, len(rows))
	for _, p := range rows {
		byID[p.ID] = p
	}
	paths := map[int64]string{0: ""}
	visiting := make(map[int64]bool)
	var resolve func(int64) (string, error)
	resolve = func(id int64) (string, error) {
		if path, ok := paths[id]; ok {
			return path, nil
		}
		p, ok := byID[id]
		if !ok || visiting[id] {
			return "", &progress.Error{Code: "playlist_order_invalid", Cause: fmt.Errorf("invalid playlist ancestry at %d", id)}
		}
		visiting[id] = true
		parent, err := resolve(p.ParentListID)
		if err != nil {
			return "", err
		}
		paths[id] = parent + p.Title + ";"
		delete(visiting, id)
		return paths[id], nil
	}
	for _, p := range rows {
		if _, err := resolve(p.ID); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func hasSmartlistTable(ctx context.Context, tx *sql.Tx) (bool, error) {
	var objectType string
	err := tx.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name='Smartlist'`).Scan(&objectType)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if objectType != "table" {
		return false, fmt.Errorf("Smartlist schema object is %q, not a table", objectType)
	}
	return true, nil
}

func rewriteSmartlistPaths(ctx context.Context, tx *sql.Tx, before, after map[int64]string) error {
	exists, err := hasSmartlistTable(ctx, tx)
	if err != nil || !exists {
		return err
	}
	changes := make(map[string]string)
	for id, old := range before {
		path, exists := after[id]
		if !exists || id == 0 || path == old {
			continue
		}
		changes[old] = path
	}
	if len(changes) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT listUuid,parentPlaylistPath,nextPlaylistPath FROM Smartlist ORDER BY listUuid`)
	if err != nil {
		return err
	}
	type link struct{ id, parent, next string }
	var links []link
	for rows.Next() {
		var value link
		if err := rows.Scan(&value.id, &value.parent, &value.next); err != nil {
			rows.Close()
			return err
		}
		links = append(links, value)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Read the original values before writing: a new path can also be another
	// old path when a title contains Engine's unescaped semicolon separator.
	for _, value := range links {
		parent, next := value.parent, value.next
		if path, ok := changes[parent]; ok {
			parent = path
		}
		if path, ok := changes[next]; ok {
			next = path
		}
		if parent == value.parent && next == value.next {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE Smartlist SET parentPlaylistPath=?,nextPlaylistPath=? WHERE listUuid=?`, parent, next, value.id); err != nil {
			return fmt.Errorf("updating Smartlist paths: %w", err)
		}
	}
	return nil
}

// The head is the furthest predecessor of the Smartlist ending at nextPath.
// A cycle is invalid saved order, not permission to loop indefinitely.
func smartlistChainHead(ctx context.Context, tx *sql.Tx, parent, nextPath string) (string, error) {
	var current string
	err := tx.QueryRowContext(ctx, `SELECT listUuid FROM Smartlist WHERE parentPlaylistPath=? AND nextPlaylistPath=? AND nextListUuid=''`, parent, nextPath).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	seen := make(map[string]bool)
	for {
		if seen[current] {
			return "", &progress.Error{Code: "playlist_order_invalid", Cause: fmt.Errorf("cyclic Smartlist sibling order")}
		}
		seen[current] = true
		var previous string
		err := tx.QueryRowContext(ctx, `SELECT listUuid FROM Smartlist WHERE parentPlaylistPath=? AND nextListUuid=?`, parent, current).Scan(&previous)
		if errors.Is(err, sql.ErrNoRows) {
			return current, nil
		}
		if err != nil {
			return "", err
		}
		current = previous
	}
}

func unlinkSmartlistPredecessor(ctx context.Context, tx *sql.Tx, source playlistRow, paths map[int64]string) error {
	exists, err := hasSmartlistTable(ctx, tx)
	if err != nil || !exists {
		return err
	}
	nextPath := paths[source.next.Int64]
	head, err := smartlistChainHead(ctx, tx, paths[source.ParentListID], nextPath)
	if err != nil {
		return fmt.Errorf("finding following Smartlist chain: %w", err)
	}
	if head != "" {
		nextPath = ""
	}
	_, err = tx.ExecContext(ctx, `UPDATE Smartlist SET nextPlaylistPath=?,nextListUuid=? WHERE nextPlaylistPath=? AND nextListUuid=''`, nextPath, head, paths[source.ID])
	return err
}

// RenamePlaylist updates both Engine's title and every affected Smartlist path.
func RenamePlaylist(ctx context.Context, database *sql.DB, id int64, title string) error {
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
	before, err := playlistPaths(rows)
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].ID == id {
			rows[i].Title = title
		}
	}
	after, err := playlistPaths(rows)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE Playlist SET title=?,lastEditTime=CURRENT_TIMESTAMP WHERE id=?`, title, id); err != nil {
		return err
	}
	if err := rewriteSmartlistPaths(ctx, tx, before, after); err != nil {
		return err
	}
	return tx.Commit()
}
