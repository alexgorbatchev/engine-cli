// Package intakecontrol owns portable queue suspension across storage replacement.
// It is shared by intake and replacement owners without importing the worker.
package intakecontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// Reader is implemented by both the queue connection and its validation transaction.
type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type State struct {
	Paused bool   `json:"paused"`
	Reason string `json:"reason,omitempty"`
}

func Create(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE QueueControl (
 id INTEGER PRIMARY KEY CHECK(id=1),
 reason TEXT NOT NULL CHECK(reason IN ('','manual','restored','history_unavailable'))
) STRICT; INSERT INTO QueueControl(id,reason) VALUES(1,'')`)
	return err
}

func Read(ctx context.Context, d Reader) (State, error) {
	var state State
	if err := d.QueryRowContext(ctx, `SELECT reason FROM QueueControl WHERE id=1`).Scan(&state.Reason); err != nil {
		return state, err
	}
	switch state.Reason {
	case "", "manual", "restored", "history_unavailable":
		state.Paused = state.Reason != ""
		return state, nil
	default:
		return State{}, fmt.Errorf("invalid intake suspension reason")
	}
}

func Set(ctx context.Context, d *sql.DB, reason string) error {
	result, err := d.ExecContext(ctx, `UPDATE QueueControl SET reason=? WHERE id=1`, reason)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("intake suspension record is missing")
	}
	return nil
}

// Restored pauses every replacement, including same-machine older snapshots.
// Replacement owners must stop and join the worker before replacing storage;
// this marker prevents a later process from restarting machine-specific paths.
func Restored(ctx context.Context, library string) (err error) {
	path, err := companion.Database(library, companion.IntakeName)
	if err != nil || path == "" {
		return err
	}
	d, err := db.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	return Set(ctx, d, "restored")
}
