package history

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"zombiezen.com/go/sqlite"
)

// A patch changes only native linkage columns. The source database fingerprint
// binds the complete rows, including rules and columns unknown to this reader.
// A nil after removes the row; before is always its exact observed linkage.
type smartlistPatch struct {
	before db.SmartlistRecord
	after  *db.SmartlistRecord
}

// stageSmartlistImage never publishes to a library or historical snapshot. Its
// caller owns a new destination and must journal all publication separately.
// An empty patch does not create any file or directory.
func stageSmartlistImage(ctx context.Context, source, destination, expected string, patches []smartlistPatch) (_ string, err error) {
	return stageSmartlistRows(ctx, source, destination, expected, patches, func(_, after *db.SmartlistInventory) error {
		if len(after.Issues) != 0 {
			return errors.New("staged Smartlist image still contains damaged links")
		}
		return nil
	})
}

// Both explicit stage owners retain the same fingerprint, full-row, schema and
// native changeset checks. Their graph proof is mandatory, not a skip flag.
func stageSmartlistRows(ctx context.Context, source, destination, expected string, patches []smartlistPatch, validate func(before, after *db.SmartlistInventory) error) (_ string, err error) {
	if len(patches) == 0 {
		return "", nil
	}
	if expected == "" {
		return "", errors.New("missing Smartlist source fingerprint")
	}
	initial, err := fingerprint(ctx, source)
	if err != nil {
		return "", err
	}
	if initial != expected {
		return "", smartlistAmbiguous("Smartlist source changed before staging")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(destination + suffix); err == nil {
			return "", errors.New("Smartlist stage destination has an existing sidecar")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	// Only this exclusively created stage belongs to us. Never remove source or
	// an existing destination when validation, backup or transaction fails.
	defer func() {
		if err != nil {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				if removeErr := os.Remove(destination + suffix); removeErr != nil && !os.IsNotExist(removeErr) {
					err = errors.Join(err, removeErr)
				}
			}
		}
	}()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := copyDatabase(ctx, source, destination); err != nil {
		return "", err
	}
	staged, err := fingerprint(ctx, destination)
	if err != nil {
		return "", err
	}
	current, err := fingerprint(ctx, source)
	if err != nil {
		return "", err
	}
	if current != expected || staged != expected {
		return "", smartlistAmbiguous("Smartlist source changed during staging")
	}
	d, err := db.Open(destination)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	before, err := db.ReadSmartlistInventory(ctx, d)
	if err != nil {
		return "", err
	}
	columns, originalRows, err := readCompleteSmartlistRows(ctx, d)
	if err != nil {
		return "", err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	seen := make(map[string]bool, len(patches))
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if seen[patch.before.UUID] {
			return "", errors.New("duplicate Smartlist patch identity")
		}
		seen[patch.before.UUID] = true
		var actual db.SmartlistRecord
		if err := tx.QueryRowContext(ctx, `SELECT listUuid,COALESCE(title,''),COALESCE(parentPlaylistPath,''),COALESCE(nextPlaylistPath,''),COALESCE(nextListUuid,'') FROM Smartlist WHERE listUuid=?`, patch.before.UUID).Scan(&actual.UUID, &actual.Name, &actual.ParentPath, &actual.NextPath, &actual.NextUUID); err != nil {
			return "", err
		}
		if actual != patch.before {
			return "", smartlistAmbiguous("Smartlist row does not match reviewed linkage")
		}
		query := `DELETE FROM Smartlist WHERE listUuid=?`
		args := []any{actual.UUID}
		if patch.after != nil {
			if patch.after.UUID != actual.UUID || patch.after.Name != actual.Name {
				return "", errors.New("Smartlist patch cannot change identity or name")
			}
			query = `UPDATE Smartlist SET parentPlaylistPath=?,nextPlaylistPath=?,nextListUuid=? WHERE listUuid=?`
			args = []any{nil, nil, nil, actual.UUID}
			for index, column := range columns {
				switch column {
				case "parentPlaylistPath":
					if actual.ParentPath != patch.after.ParentPath {
						originalRows[actual.UUID][index] = patch.after.ParentPath
					}
					args[0] = originalRows[actual.UUID][index]
				case "nextPlaylistPath":
					if actual.NextPath != patch.after.NextPath {
						originalRows[actual.UUID][index] = patch.after.NextPath
					}
					args[1] = originalRows[actual.UUID][index]
				case "nextListUuid":
					if actual.NextUUID != patch.after.NextUUID {
						originalRows[actual.UUID][index] = patch.after.NextUUID
					}
					args[2] = originalRows[actual.UUID][index]
				}
			}
		} else {
			delete(originalRows, actual.UUID)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return "", err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		if count != 1 {
			return "", fmt.Errorf("Smartlist patch affected %d rows instead of one", count)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	afterColumns, afterRows, err := readCompleteSmartlistRows(ctx, d)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(columns, afterColumns) || !reflect.DeepEqual(originalRows, afterRows) {
		return "", errors.New("Smartlist stage changed unplanned native row contents")
	}
	inventory, err := db.ReadSmartlistInventory(ctx, d)
	if err != nil {
		return "", err
	}
	if err := validate(before, inventory); err != nil {
		return "", err
	}
	if err := validateSmartlistOnlyChanges(ctx, source, destination); err != nil {
		return "", err
	}
	current, err = fingerprint(ctx, source)
	if err != nil {
		return "", err
	}
	if current != expected {
		return "", smartlistAmbiguous("Smartlist source changed while applying staged patches")
	}
	return fingerprint(ctx, destination)
}

func validateSmartlistOnlyChanges(ctx context.Context, source, destination string) error {
	delta, err := changeset(ctx, source, destination)
	if err != nil {
		return err
	}
	if delta == nil {
		return errors.New("Smartlist stage cannot prove a row-scoped change")
	}
	it, err := sqlite.NewChangesetIterator(bytes.NewReader(delta))
	if err != nil {
		return err
	}
	defer it.Close()
	for {
		more, err := it.Next()
		if err != nil || !more {
			return err
		}
		op, err := it.Operation()
		if err != nil {
			return err
		}
		if op.TableName != "Smartlist" {
			return errors.New("Smartlist stage changed another native table")
		}
	}
}

func readCompleteSmartlistRows(ctx context.Context, database *sql.DB) ([]string, map[string][]any, error) {
	rows, err := database.QueryContext(ctx, `SELECT * FROM Smartlist ORDER BY listUuid`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	identity := -1
	for i, column := range columns {
		if column == "listUuid" {
			identity = i
		}
	}
	if identity < 0 {
		return nil, nil, errors.New("missing Smartlist identity column")
	}
	result := make(map[string][]any)
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, nil, err
		}
		id, ok := values[identity].(string)
		if !ok {
			return nil, nil, errors.New("invalid Smartlist identity value")
		}
		if _, duplicate := result[id]; duplicate {
			return nil, nil, errors.New("duplicate Smartlist identity value")
		}
		for i, value := range values {
			if blob, ok := value.([]byte); ok {
				clone := make([]byte, len(blob))
				copy(clone, blob)
				values[i] = clone
			}
		}
		result[id] = values
	}
	return columns, result, errors.Join(rows.Err(), rows.Close())
}
