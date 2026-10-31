package colors

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const schema = `
CREATE TABLE Metadata(id INTEGER NOT NULL PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL);
INSERT INTO Metadata VALUES(1,1);
CREATE TABLE TrackColor(originUUID TEXT NOT NULL CHECK(length(originUUID)>0),
 originTrackId INTEGER NOT NULL CHECK(originTrackId>0),color TEXT NOT NULL,
 PRIMARY KEY(originUUID,originTrackId));
`

func storageError(err error) error {
	var coded *progress.Error
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &coded) {
		return err
	}
	return &progress.Error{Code: "color_store_unavailable", Cause: err}
}

// Read never enrolls storage. Its revision covers stored assignments rather than
// a projection onto the current Engine collection.
func Read(ctx context.Context, library string) (state State, err error) {
	defer func() { err = storageError(err) }()
	path, err := companion.Database(library, companion.ColorsName)
	if err != nil {
		return State{}, err
	}
	if path == "" {
		return State{Assignments: []Assignment{}}, ctx.Err()
	}
	return readDatabase(ctx, path)
}

func readDatabase(ctx context.Context, path string) (State, error) {
	d, err := db.Open(path, true)
	if err != nil {
		return State{}, err
	}
	defer d.Close()
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	return readState(ctx, tx)
}

// ValidateDatabase validates a native snapshot before collection restoration.
func ValidateDatabase(ctx context.Context, path string) error {
	_, err := readDatabase(ctx, path)
	return storageError(err)
}

func readState(ctx context.Context, tx *sql.Tx) (State, error) {
	s := State{Assignments: []Assignment{}}
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT version FROM Metadata WHERE id=1").Scan(&version); err != nil {
		return s, err
	}
	if version != 1 {
		return s, failure("color_store_unavailable", "track-color storage has an unsupported schema")
	}
	rows, err := tx.QueryContext(ctx, "SELECT originUUID,originTrackId,color FROM TrackColor ORDER BY originUUID,originTrackId")
	if err != nil {
		return s, err
	}
	defer rows.Close()
	seen := map[companion.Origin]bool{}
	for rows.Next() {
		var assignment Assignment
		if err := rows.Scan(&assignment.OriginUUID, &assignment.OriginTrackID, &assignment.Color); err != nil {
			return s, err
		}
		color, err := normalize(assignment.Color)
		origin := companion.Origin{UUID: assignment.OriginUUID, ID: assignment.OriginTrackID}
		if err != nil || color == "" || color != assignment.Color || origin.UUID == "" || origin.ID <= 0 || seen[origin] {
			return s, failure("color_store_unavailable", "track-color storage contains an invalid assignment")
		}
		seen[origin] = true
		s.Assignments = append(s.Assignments, assignment)
	}
	return s, rows.Err()
}

// Replace publishes a validated transfer/lifecycle state. The caller owns the
// collection write barrier and before-image; this transaction owns only colors.
func Replace(ctx context.Context, library, expected string, next State) (result State, err error) {
	return replaceChecked(ctx, library, expected, next, nil)
}

func replaceChecked(ctx context.Context, library, expected string, next State, check func(State) error) (result State, err error) {
	defer func() { err = storageError(err) }()
	state, err := Read(ctx, library)
	if err != nil {
		return State{}, err
	}
	if err := checkRevision(expected, state); err != nil {
		return State{}, err
	}
	if err := validateState(next); err != nil {
		return State{}, err
	}
	next = canonical(next)
	if state.Revision() == next.Revision() {
		return state, nil
	}
	d, created, err := companion.OpenWrite(ctx, library, companion.ColorsName)
	if err != nil {
		return State{}, err
	}
	committed := false
	defer func() {
		err = errors.Join(err, d.Close())
		if created && !committed {
			err = errors.Join(err, companion.Remove(companion.ColorsPath(library)))
		}
	}()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	if created {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return State{}, err
		}
	}
	state, err = readState(ctx, tx)
	if err != nil {
		return State{}, err
	}
	if err := checkRevision(expected, state); err != nil {
		return State{}, err
	}
	if check != nil {
		if err := check(state); err != nil {
			return State{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM TrackColor"); err != nil {
		return State{}, err
	}
	for _, assignment := range next.Assignments {
		if _, err := tx.ExecContext(ctx, "INSERT INTO TrackColor VALUES(?,?,?)", assignment.OriginUUID, assignment.OriginTrackID, assignment.Color); err != nil {
			return State{}, err
		}
	}
	result, err = readState(ctx, tx)
	if err != nil {
		return State{}, err
	}
	err = tx.Commit()
	committed = err == nil
	return result, err
}

func validateState(state State) error {
	seen := map[companion.Origin]bool{}
	for _, assignment := range state.Assignments {
		color, err := normalize(assignment.Color)
		key := companion.Origin{UUID: assignment.OriginUUID, ID: assignment.OriginTrackID}
		if err != nil || color == "" || color != assignment.Color || key.UUID == "" || key.ID <= 0 || seen[key] {
			return failure("color_invalid", "track-color assignments require unique valid origins and canonical RGB colors")
		}
		seen[key] = true
	}
	return nil
}
