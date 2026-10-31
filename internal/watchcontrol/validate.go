package watchcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// ValidateDatabase validates a portable discovery image and its exact
// queue linkage before a replacement owner touches either live companion.
// An unlinked pending handoff is a valid crash recovery state.
func ValidateDatabase(ctx context.Context, path, queuePath string) (err error) {
	d, err := db.Open(path, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := Read(ctx, tx); err != nil {
		return err
	}
	var integrity string
	if err := tx.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("watched-folder image integrity check failed")
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	if rows.Next() {
		return errors.Join(fmt.Errorf("watched-folder image has an orphaned journal record"), rows.Close())
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var invalid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM WatchHandoff h
 JOIN WatchVersion v ON v.id=h.version_id
 WHERE h.source_identity<>v.identity OR h.source_size<>v.size OR h.source_modified<>v.modified
 OR h.source_path='' OR h.source_identity='') OR EXISTS(
 SELECT 1 FROM WatchRoot WHERE path='' OR identity='') OR EXISTS(
 SELECT 1 FROM WatchVersion WHERE identity='') OR EXISTS(
 SELECT 1 FROM WatchAlias WHERE path='')`).Scan(&invalid); err != nil {
		return err
	}
	if invalid {
		return fmt.Errorf("watched-folder image native version and receipt disagree")
	}
	rows, err = tx.QueryContext(ctx, `SELECT item_id,source_path,source_identity,source_size,source_modified
 FROM WatchHandoff WHERE item_id IS NOT NULL ORDER BY version_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var queue *sql.DB
	if queuePath != "" {
		queue, err = db.Open(queuePath, true)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, queue.Close()) }()
	}
	for rows.Next() {
		var id, size, modified int64
		var path, identity string
		if err := rows.Scan(&id, &path, &identity, &size, &modified); err != nil {
			return err
		}
		if queue == nil {
			return fmt.Errorf("linked watched-folder image requires its queue image")
		}
		var matches bool
		if err := queue.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM Item
 WHERE id=? AND source_path=? AND source_identity=? AND source_size=? AND source_modified=?)`,
			id, path, identity, size, modified).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("watched-folder receipt differs from its immutable queue item")
		}
	}
	return rows.Err()
}
