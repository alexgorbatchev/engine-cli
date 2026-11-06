package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// SmartlistProjection retains complete native values while interpreting links
// through native playlist identities. Its rows and blobs belong to the caller.
type SmartlistProjection struct {
	Columns        []string
	Rows           []SmartlistProjectionRow
	Actions        []SmartlistRepairRow
	EvidenceDigest string
}

type SmartlistProjectionRow struct {
	Values           map[string]any
	ParentPlaylistID int64
	NextPlaylistID   int64
}

// ReadSmartlistProjection observes source collection rows and history under one
// publication lease and native read transaction. The database must be opened
// read-only. Read must not mutate storage or retain the transaction: first-time
// history enrollment can discard its result and retry the complete observation.
func ReadSmartlistProjection[T any](ctx context.Context, lib string, database *sql.DB, read func(context.Context, *sql.Tx, *SmartlistProjection) (T, error)) (result T, err error) {
	if database == nil || read == nil {
		return result, errors.New("Smartlist projection requires a database and reader")
	}
	return ReadSmartlistSafe(ctx, lib, func(ctx context.Context) (result T, err error) {
		tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return result, err
		}
		defer func() {
			if rollbackErr := tx.Rollback(); !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, rollbackErr)
			}
		}()
		projection, err := projectSmartlists(ctx, lib, tx)
		if err != nil {
			return result, err
		}
		return read(ctx, tx, projection)
	})
}

// The caller owns either the history publication lease or its exclusive writer
// lock. Keep this constructor shared without adding a public lock-bypass flag.
func projectSmartlists(ctx context.Context, lib string, tx db.Reader) (*SmartlistProjection, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	lib = filepath.Dir(filepath.Dir(dir))
	inventory, err := db.ReadSmartlistInventoryFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	previous, err := entries(dir)
	if err != nil {
		return nil, err
	}
	for _, pending := range previous {
		if pending.After == nil {
			return nil, &progress.Error{Code: "history_recovery_required", Cause: fmt.Errorf("operation %s is incomplete", pending.ID)}
		}
	}
	evidence, err := resolveSmartlistEvidence(ctx, filepath.Join(lib, "Database2", "m.db"), inventory, previous)
	if err != nil {
		return nil, err
	}
	orders, err := smartlistOrderFromEntries(ctx, lib, evidence, previous)
	if err != nil {
		return nil, err
	}
	links, err := planSmartlistLinks(ctx, evidence, orders)
	if err != nil {
		return nil, err
	}
	projection := &SmartlistProjection{Rows: []SmartlistProjectionRow{}, Actions: []SmartlistRepairRow{}}
	var original map[string][]any
	if inventory.HasSmartlist {
		projection.Columns, original, err = readCompleteSmartlistRows(ctx, tx)
		if err != nil {
			return nil, err
		}
	}
	projection.EvidenceDigest, err = smartlistProjectionDigest(ctx, dir, tx, inventory, projection.Columns, original)
	if err != nil {
		return nil, err
	}
	return projectSmartlistRows(inventory, links, projection, original)
}
