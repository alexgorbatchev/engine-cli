package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const schemaVersion = 1

const schema = `
CREATE TABLE Metadata (id INTEGER NOT NULL PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL);
INSERT INTO Metadata VALUES(1,1);
CREATE TABLE Category (id TEXT NOT NULL PRIMARY KEY, name TEXT NOT NULL, nameKey TEXT NOT NULL UNIQUE);
CREATE TABLE Value (id TEXT NOT NULL PRIMARY KEY, categoryId TEXT NOT NULL REFERENCES Category(id) ON DELETE CASCADE,
 name TEXT NOT NULL, nameKey TEXT NOT NULL, UNIQUE(categoryId,nameKey));
CREATE TABLE Assignment (originUUID TEXT NOT NULL, originTrackId INTEGER NOT NULL CHECK(originTrackId>0),
 valueId TEXT NOT NULL REFERENCES Value(id) ON DELETE CASCADE, PRIMARY KEY(originUUID,originTrackId,valueId));
CREATE INDEX Assignment_value ON Assignment(valueId);
`

// Read never enrolls a companion database merely because a library was browsed.
func Read(ctx context.Context, library string) (state State, err error) {
	defer func() { err = storageError(err) }()
	paths, err := companion.Databases(library)
	if err != nil {
		return State{}, err
	}
	if len(paths) == 0 {
		return emptyState(), ctx.Err()
	}
	d, err := db.Open(paths[0], true)
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

func storageError(err error) error {
	var coded *progress.Error
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &coded) {
		return err
	}
	return &progress.Error{Code: "tag_store_unavailable", Cause: err}
}

func emptyState() State {
	return State{Categories: []Category{}, Assignments: []Assignment{}}
}

// ValidateDatabase checks a snapshot before any live Engine files are restored.
func ValidateDatabase(ctx context.Context, path string) error {
	d, err := db.Open(path, true)
	if err != nil {
		return err
	}
	defer d.Close()
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := readState(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return failure("tag_store_unavailable", "custom-tag storage has invalid references")
	}
	return rows.Err()
}

func readState(ctx context.Context, tx *sql.Tx) (State, error) {
	s := emptyState()
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT version FROM Metadata WHERE id=1").Scan(&version); err != nil {
		return s, fmt.Errorf("reading custom-tag schema: %w", err)
	}
	if version != schemaVersion {
		return s, failure("tag_store_unavailable", "custom-tag storage has an unsupported schema")
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,name FROM Category ORDER BY id")
	if err != nil {
		return s, err
	}
	byID := map[string]int{}
	for rows.Next() {
		c := Category{Values: []Value{}}
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			rows.Close()
			return s, err
		}
		byID[c.ID] = len(s.Categories)
		s.Categories = append(s.Categories, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return s, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT v.id,v.categoryId,v.name,COUNT(a.valueId) FROM Value v
 LEFT JOIN Assignment a ON a.valueId=v.id GROUP BY v.id ORDER BY v.id`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var v Value
		var category string
		if err := rows.Scan(&v.ID, &category, &v.Name, &v.TrackCount); err != nil {
			rows.Close()
			return s, err
		}
		i, ok := byID[category]
		if !ok {
			rows.Close()
			return s, failure("tag_store_unavailable", "custom-tag values reference a missing category")
		}
		s.Categories[i].Values = append(s.Categories[i].Values, v)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return s, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT originUUID,originTrackId,valueId FROM Assignment ORDER BY originUUID,originTrackId,valueId")
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.OriginUUID, &a.OriginTrackID, &a.ValueID); err != nil {
			return s, err
		}
		s.Assignments = append(s.Assignments, a)
	}
	return s, rows.Err()
}

func openWrite(ctx context.Context, library string) (*sql.DB, bool, error) {
	paths, err := companion.Databases(library)
	if err != nil {
		return nil, false, err
	}
	path := companion.TagsPath(library)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	created := len(paths) == 0
	if created {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, false, err
		}
		if err := file.Close(); err != nil {
			return nil, false, errors.Join(err, removeStore(path))
		}
	}
	d, err := db.Open(path)
	if err != nil {
		if created {
			err = errors.Join(err, removeStore(path))
		}
		return nil, false, err
	}
	return d, created, nil
}

func removeStore(path string) error {
	var result error
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func List(ctx context.Context, library string) (*Taxonomy, error) {
	s, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	storedRevision := revision(s)
	if len(s.Assignments) == 0 {
		return &Taxonomy{Revision: storedRevision, Categories: s.Categories}, nil
	}
	present, err := readOrigins(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, err
	}
	_, unavailable := missingAssignments(s, present)
	counts := map[string]map[TrackOrigin]bool{}
	for _, a := range s.Assignments {
		origin := TrackOrigin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		if present[origin] {
			if counts[a.ValueID] == nil {
				counts[a.ValueID] = map[TrackOrigin]bool{}
			}
			counts[a.ValueID][origin] = true
		}
	}
	for i := range s.Categories {
		for j := range s.Categories[i].Values {
			v := &s.Categories[i].Values[j]
			v.TrackCount = len(counts[v.ID])
		}
	}
	return &Taxonomy{Revision: storedRevision, Categories: s.Categories, UnavailableTracks: unavailable}, nil
}
