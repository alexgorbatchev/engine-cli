package intake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
)

// ValidateDatabase observes a portable queue image without enrolling storage
// or running its worker. Replacement owners validate before touching live data.
func ValidateDatabase(ctx context.Context, path string) (err error) {
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
	var application, version int
	if err := tx.QueryRowContext(ctx, `SELECT application_id,user_version FROM pragma_application_id,pragma_user_version`).Scan(&application, &version); err != nil {
		return err
	}
	if application != applicationID || version != schemaVersion {
		return fmt.Errorf("not a supported Engine intake database")
	}
	var integrity string
	if err := tx.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("intake database integrity check failed")
	}
	if _, err := intakecontrol.Read(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+itemColumns+" FROM Item ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		if _, err := scanItem(rows); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	// Read the receipt projection even when it is empty: matching PRAGMAs do
	// not make a truncated schema a usable recovery image.
	rows, err = tx.QueryContext(ctx, `SELECT item_id,history_id,plan_token,to_playlist,track_id FROM ImportIntent`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var historyID, token, playlist string
		var trackID sql.NullInt64
		if err := rows.Scan(&id, &historyID, &token, &playlist, &trackID); err != nil {
			return errors.Join(err, rows.Close())
		}
		if id <= 0 || historyID == "" || token == "" || trackID.Valid && trackID.Int64 <= 0 {
			return errors.Join(fmt.Errorf("intake import receipt is invalid"), rows.Close())
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var inconsistent bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM Item i LEFT JOIN ImportIntent r ON r.item_id=i.id
 WHERE (i.history_id<>'' AND (r.item_id IS NULL OR r.history_id<>i.history_id
        OR i.status NOT IN ('importing','imported','cancelled')))
    OR (i.status IN ('importing','imported') AND i.history_id='')
    OR (r.item_id IS NOT NULL AND i.history_id='')
    OR (i.status='imported' AND r.track_id IS NULL)
)`).Scan(&inconsistent)
	if err != nil {
		return err
	}
	if inconsistent {
		return fmt.Errorf("intake item and import receipt disagree")
	}
	rows, err = tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	if rows.Next() {
		return errors.Join(fmt.Errorf("intake receipt has no matching queue item"), rows.Close())
	}
	return errors.Join(rows.Err(), rows.Close())
}
