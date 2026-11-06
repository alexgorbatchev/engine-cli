package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// Run returns the same comparison for CLI and HTTP. The source native/history
// snapshot finishes before destination ownership, including intake exclusion.
func Run(ctx context.Context, o Options) (*Report, error) {
	o, err := Normalize(o)
	if err != nil {
		return nil, err
	}
	progress.Stage(ctx, "sync.compare", 5)
	source, err := observeSource(ctx, &o)
	if err != nil {
		return nil, err
	}
	report, err := previewSync(ctx, o, source)
	if err != nil || o.DryRun {
		return report, err
	}
	if !report.Applicable {
		return report, syncConflicts(report)
	}
	// Bind even tokenless application to this observation, then validate again
	// under destination ownership before repair, backup or history snapshots.
	o.PlanToken = report.PlanToken
	return applySync(ctx, o, source)
}

func syncConflicts(report *Report) error {
	return &progress.Error{Code: "sync_conflicts", Cause: fmt.Errorf("sync has %d unresolved conflicts; preview and resolve them before applying", len(report.Conflicts))}
}

func previewSync(ctx context.Context, o Options, source *collection) (*Report, error) {
	database, err := openLibrary(o.Destination, false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	return history.ReadSmartlistProjection(ctx, o.Destination, database, func(ctx context.Context, tx *sql.Tx, projection *history.SmartlistProjection) (*Report, error) {
		destination, err := readObservedCollection(ctx, tx, o.Destination, projection)
		if err != nil {
			return nil, err
		}
		p, err := buildPlan(ctx, o, source, destination)
		if err != nil {
			return nil, err
		}
		if o.PlanToken != "" && o.PlanToken != p.report.PlanToken {
			return p.report, staleSync()
		}
		return p.report, nil
	})
}

func applySync(ctx context.Context, o Options, source *collection) (report *Report, err error) {
	lease, err := intakelease.Replace(ctx, o.Destination)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	if o.CheckMutation != nil {
		err = o.CheckMutation(ctx)
	} else {
		var observed *systemcheck.Report
		observed, err = systemcheck.Run(ctx, nil)
		if err == nil {
			err = systemcheck.AllowMutation(observed)
		}
	}
	if err != nil {
		return nil, err
	}
	database, err := openLibrary(o.Destination, true)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	var accepted *plan
	var destinationDigest string
	entry, err := history.RunPrepared(ctx, o.Destination, history.RunOptions{
		Operation: "library.sync", Scope: history.ScopeCollection, RepairSmartlists: true,
		ObserveSmartlists: func(ctx context.Context, tx *sql.Tx, projection *history.SmartlistProjection) (bool, error) {
			destination, err := readObservedCollection(ctx, tx, o.Destination, projection)
			if err != nil {
				return false, err
			}
			destinationDigest = observationDigest(destination)
			accepted, err = buildPlan(ctx, o, source, destination)
			if err != nil {
				return false, err
			}
			report = accepted.report
			if o.PlanToken != "" && o.PlanToken != report.PlanToken {
				return false, staleSync()
			}
			if !report.Applicable {
				return false, syncConflicts(report)
			}
			if len(accepted.desired) == 0 && len(accepted.remove) == 0 && len(accepted.copy) == 0 && len(accepted.artwork) == 0 && len(projection.Actions) == 0 && reflect.DeepEqual(destination.State.peer(source), accepted.baseline) {
				return false, nil
			}
			return true, nil
		},
		Prepare: func(ctx context.Context) error {
			tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return err
			}
			err = checkDestinationObservation(ctx, tx, o.Destination, destinationDigest)
			err = errors.Join(err, tx.Rollback())
			if err != nil {
				return err
			}
			progress.Stage(ctx, "sync.backup", 20)
			_, err = backup.AutoBackupIfNeeded(ctx, o.Destination, o.NoBackup)
			return err
		},
	}, func(ctx context.Context) (err error) {
		conn, err := database.Conn(ctx)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, conn.Close()) }()
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() {
			if rollbackErr := tx.Rollback(); !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, rollbackErr)
			}
		}()
		if err := checkDestinationObservation(ctx, tx, o.Destination, destinationDigest); err != nil {
			return err
		}
		return applyPlan(ctx, o, accepted, tx)
	})
	if entry != nil && report != nil {
		report.OperationID = entry.ID
	}
	return report, err
}
