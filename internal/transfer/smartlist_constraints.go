package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Check declared native constraints with the observed destination table. The
// isolated database preserves SQL affinity, collation, NULL and BLOB semantics
// without writing either library or replaying destination triggers.
func (p *plan) checkSmartlistConstraints(ctx context.Context) (err error) {
	if len(p.expectedSmartlists) == 0 {
		return ctx.Err()
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if _, err := tx.ExecContext(ctx, p.destination.SmartlistSchema); err != nil {
		return fmt.Errorf("check destination Smartlist schema: %w", err)
	}
	columns, names, marks := []string{}, []string{}, []string{}
	for _, column := range p.destination.Schema["Smartlist"] {
		if column != "lastEditTime" {
			columns = append(columns, column)
			names = append(names, quoted(column))
			marks = append(marks, "?")
		}
	}
	// Every planned row must survive. Override an observed IGNORE or REPLACE
	// policy so a collision cannot silently omit or overwrite an earlier row.
	insert := "INSERT OR ABORT INTO Smartlist (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	for _, key := range orderedKeys(p.expectedSmartlists) {
		e := p.expectedSmartlists[key]
		values := make([]any, len(columns))
		for index, column := range columns {
			values[index] = e.Values[column]
		}
		if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
			var native *sqlite.Error
			if !errors.As(err, &native) || native.Code()&0xff != sqlite3.SQLITE_CONSTRAINT {
				return err
			}
			p.conflict(savedChange(e), "sync_smartlist_constraint_conflict", "final Smartlist rows violate destination constraints; check for duplicate names in the same folder")
		}
	}
	return nil
}
