package importsource

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const schemaVersion = 1
const schema = `
CREATE TABLE Metadata(id INTEGER PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL);
INSERT INTO Metadata VALUES(1,1);
CREATE TABLE Document(id TEXT PRIMARY KEY NOT NULL,xml BLOB NOT NULL);
CREATE TABLE Track(documentId TEXT NOT NULL REFERENCES Document(id),sourceId TEXT NOT NULL,
 originUUID TEXT NOT NULL,originTrackId INTEGER NOT NULL CHECK(originTrackId>0),
 PRIMARY KEY(documentId,sourceId));
CREATE INDEX Track_origin ON Track(originUUID,originTrackId);
`

func storageError(err error) error {
	var coded *progress.Error
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &coded) {
		return err
	}
	return &progress.Error{Code: "import_store_unavailable", Cause: err}
}

// Read is passive: neither browsing nor previewing enrolls or repairs storage.
func Read(ctx context.Context, library string) (s State, err error) {
	defer func() { err = storageError(err) }()
	path, err := companion.Database(library, companion.ImportsName)
	if err != nil {
		return State{}, err
	}
	if path == "" {
		return emptyState(), ctx.Err()
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

// ValidateDatabase runs before live restore publication, including digest and
// reference validation; a valid SQLite container alone is insufficient.
func ValidateDatabase(ctx context.Context, path string) error {
	_, err := readDatabase(ctx, path)
	return storageError(err)
}

func readState(ctx context.Context, tx *sql.Tx) (State, error) {
	s := emptyState()
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT version FROM Metadata WHERE id=1").Scan(&version); err != nil {
		return s, err
	}
	if version != schemaVersion {
		return s, failure("import_store_unavailable", "source archive has an unsupported schema")
	}
	if err := readDocuments(ctx, tx, &s); err != nil {
		return s, err
	}
	if err := readTracks(ctx, tx, &s); err != nil {
		return s, err
	}
	return canonical(s)
}

func readDocuments(ctx context.Context, tx *sql.Tx, s *State) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,xml FROM Document ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.XML); err != nil {
			return err
		}
		s.Documents = append(s.Documents, d)
	}
	return rows.Err()
}

func readTracks(ctx context.Context, tx *sql.Tx, s *State) error {
	rows, err := tx.QueryContext(ctx, "SELECT documentId,sourceId,originUUID,originTrackId FROM Track ORDER BY documentId,sourceId")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tr Track
		if err := rows.Scan(&tr.DocumentID, &tr.SourceID, &tr.OriginUUID, &tr.OriginTrackID); err != nil {
			return err
		}
		s.Tracks = append(s.Tracks, tr)
	}
	return rows.Err()
}

// Replace publishes a complete reviewed archive state inside the caller's
// collection history. Independent Engine publication requires pending recovery
// if this publication fails; callers must not report a cross-file rollback.
func Replace(ctx context.Context, library, expected string, next State) (err error) {
	defer func() { err = storageError(err) }()
	next, err = canonical(next)
	if err != nil {
		return err
	}
	previous, err := Read(ctx, library)
	if err != nil {
		return err
	}
	if err := checkRevision(expected, previous); err != nil {
		return err
	}
	if next.Revision() == previous.Revision() {
		return ctx.Err()
	}
	d, created, err := companion.OpenWrite(ctx, library, companion.ImportsName)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		err = errors.Join(err, d.Close())
		if created && !committed {
			err = errors.Join(err, companion.Remove(companion.ImportsPath(library)))
		}
	}()
	err = replaceDatabase(ctx, d, created, expected, next)
	committed = err == nil
	return err
}

func checkRevision(expected string, s State) error {
	if expected == "" || expected != s.Revision() {
		return failure("import_revision_stale", "source archive changed; review the import again")
	}
	return nil
}

func replaceDatabase(ctx context.Context, d *sql.DB, created bool, expected string, next State) error {
	c, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return err
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if created {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
	}
	previous, err := readState(ctx, tx)
	if err != nil {
		return err
	}
	if err := checkRevision(expected, previous); err != nil {
		return err
	}
	if err := writeState(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit()
}

func writeState(ctx context.Context, tx *sql.Tx, s State) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM Track; DELETE FROM Document"); err != nil {
		return err
	}
	for _, d := range s.Documents {
		if _, err := tx.ExecContext(ctx, "INSERT INTO Document VALUES(?,?)", d.ID, d.XML); err != nil {
			return err
		}
	}
	for _, tr := range s.Tracks {
		if _, err := tx.ExecContext(ctx, "INSERT INTO Track VALUES(?,?,?,?)", tr.DocumentID, tr.SourceID, tr.OriginUUID, tr.OriginTrackID); err != nil {
			return err
		}
	}
	return nil
}
