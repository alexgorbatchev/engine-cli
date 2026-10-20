package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func open(ctx context.Context, path string, flags sqlite.OpenFlags) (*sqlite.Conn, error) {
	c, err := sqlite.OpenConn(path, flags)
	if err != nil {
		return nil, err
	}
	c.SetInterrupt(ctx.Done())
	return c, nil
}

// copyDatabase uses SQLite's online backup API for both capture and publication.
func copyDatabase(ctx context.Context, src, dst string) (err error) {
	s, err := open(ctx, src, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	d, err := open(ctx, dst, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	b, err := sqlite.NewBackup(d, "main", s, "main")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, b.Close()) }()
	for more := true; more; {
		if err = ctx.Err(); err != nil {
			return err
		}
		more, err = b.Step(256)
		if err != nil {
			return err
		}
	}
	return nil
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func queryStrings(c *sqlite.Conn, q string, args ...any) ([]string, error) {
	var values []string
	err := sqlitex.ExecuteTransient(c, q, &sqlitex.ExecOptions{Args: args, ResultFunc: func(s *sqlite.Stmt) error { values = append(values, s.ColumnText(0)); return nil }})
	return values, err
}

func schema(c *sqlite.Conn) ([]string, error) {
	return queryStrings(c, `SELECT type||':'||name||':'||COALESCE(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
}

// fingerprint is logical: WAL layout and checkpoint activity are not edits.
func fingerprint(ctx context.Context, path string) (string, error) {
	c, err := open(ctx, path, sqlite.OpenReadOnly)
	if err != nil {
		return "", err
	}
	defer c.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	s, err := schema(c)
	if err != nil {
		return "", err
	}
	if err = enc.Encode(s); err != nil {
		return "", err
	}
	tables, err := queryStrings(c, `SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name`)
	if err != nil {
		return "", err
	}
	for _, table := range tables {
		cols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
		if err != nil {
			return "", err
		}
		order := make([]string, len(cols))
		for i, col := range cols {
			order[i] = quote(col)
		}
		err = sqlitex.ExecuteTransient(c, "SELECT * FROM "+quote(table)+" ORDER BY "+strings.Join(order, ","), &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error {
			values := make([]any, s.ColumnCount())
			for i := range values {
				switch s.ColumnType(i) {
				case sqlite.TypeNull:
					values[i] = nil
				case sqlite.TypeBlob:
					b, err := io.ReadAll(s.ColumnReader(i))
					if err != nil {
						return err
					}
					values[i] = b
				case sqlite.TypeFloat:
					values[i] = []string{sqlite.TypeFloat.String(), strconv.FormatFloat(s.ColumnFloat(i), 'g', -1, 64)}
				case sqlite.TypeText:
					values[i] = []string{sqlite.TypeText.String(), hex.EncodeToString([]byte(s.ColumnText(i)))}
				default:
					values[i] = []string{s.ColumnType(i).String(), s.ColumnText(i)}
				}
			}
			return enc.Encode(values)
		}})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// changeset returns nil for a database-wide change (schema or unkeyed tables).
func changeset(ctx context.Context, before, after string) ([]byte, error) {
	c, err := open(ctx, after, sqlite.OpenReadOnly)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err = sqlitex.ExecuteTransient(c, "ATTACH ? AS previous", &sqlitex.ExecOptions{Args: []any{before}}); err != nil {
		return nil, err
	}
	a, err := schema(c)
	if err != nil {
		return nil, err
	}
	b, err := queryStrings(c, `SELECT type||':'||name||':'||COALESCE(sql,'') FROM previous.sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	if !equalStrings(a, b) {
		return nil, nil
	}
	tables, err := queryStrings(c, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	session, err := c.CreateSession("main")
	if err != nil {
		return nil, err
	}
	defer session.Delete()
	for _, table := range tables {
		keys, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) WHERE pk>0", table)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return nil, nil
		}
		if err = session.Diff("previous", table); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err = session.WriteChangeset(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func restoreImage(ctx context.Context, current, before, after, dst string, force bool) error {
	delta, err := changeset(ctx, before, after)
	if err != nil {
		return err
	}
	if delta == nil {
		a, err := fingerprint(ctx, after)
		if err != nil {
			return err
		}
		b, err := fingerprint(ctx, current)
		if err != nil {
			return err
		}
		if a != b && !force {
			return fmt.Errorf("database-wide undo conflicts with later changes; inspect history before using --force")
		}
		return copyDatabase(ctx, before, dst)
	}
	if err = copyDatabase(ctx, current, dst); err != nil {
		return err
	}
	c, err := open(ctx, dst, sqlite.OpenReadWrite)
	if err != nil {
		return err
	}
	defer c.Close()
	expected, err := schema(c)
	if err != nil {
		return err
	}
	source, err := open(ctx, after, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	afterSchema, err := schema(source)
	closeErr := source.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	if !equalStrings(expected, afterSchema) {
		return fmt.Errorf("database schema changed since this operation; restore a compatible snapshot first")
	}
	if err = sqlitex.ExecuteTransient(c, "ATTACH ? AS original", &sqlitex.ExecOptions{Args: []any{before}}); err != nil {
		return err
	}
	triggers, err := queryStrings(c, `SELECT sql FROM sqlite_schema WHERE type='trigger' ORDER BY name`)
	if err != nil {
		return err
	}
	names, err := queryStrings(c, `SELECT name FROM sqlite_schema WHERE type='trigger' ORDER BY name`)
	if err != nil {
		return err
	}
	// This is an offline restoration image. Changesets already contain trigger
	// effects; replaying triggers a second time would corrupt the restored state.
	// The published image always has the original trigger definitions restored.
	for _, name := range names {
		if err = sqlitex.ExecuteTransient(c, "DROP TRIGGER "+quote(name), nil); err != nil {
			return err
		}
	}
	var conflict error
	var inverse bytes.Buffer
	// v1.4.2 ApplyInverseChangeset reads its own pipe instead of its input and
	// deadlocks. Use the separate native inversion and application APIs.
	if err = sqlite.InvertChangeset(&inverse, bytes.NewReader(delta)); err != nil {
		return err
	}
	err = c.ApplyChangeset(&inverse, nil, func(kind sqlite.ConflictType, it *sqlite.ChangesetIterator) sqlite.ConflictAction {
		op, e := it.Operation()
		if e != nil {
			conflict = e
			return sqlite.ChangesetAbort
		}
		conflict = fmt.Errorf("undo conflict in %s (%s); inspect history before using --force", op.TableName, kind)
		if !force {
			return sqlite.ChangesetAbort
		}
		switch kind {
		case sqlite.ChangesetData, sqlite.ChangesetConflict:
			conflict = nil
			return sqlite.ChangesetReplace
		case sqlite.ChangesetNotFound:
			if op.Type == sqlite.OpDelete {
				conflict = nil
				return sqlite.ChangesetOmit
			}
			if e := restoreMissing(c, it, op); e == nil {
				conflict = nil
				return sqlite.ChangesetOmit
			} else {
				conflict = e
			}
		}
		return sqlite.ChangesetAbort
	})
	if err != nil {
		return errors.Join(conflict, err)
	}
	for _, q := range triggers {
		if err = sqlitex.ExecuteTransient(c, q, nil); err != nil {
			return err
		}
	}
	return sqlitex.ExecuteTransient(c, "PRAGMA foreign_key_check", &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { return fmt.Errorf("undo would break a reference in %s", s.ColumnText(0)) }})
}

func restoreMissing(c *sqlite.Conn, it *sqlite.ChangesetIterator, op *sqlite.ChangesetOperation) error {
	cols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) ORDER BY cid", op.TableName)
	if err != nil {
		return err
	}
	keys, err := it.PrimaryKey()
	if err != nil {
		return err
	}
	var conditions []string
	var args []any
	for i, pk := range keys {
		if !pk {
			continue
		}
		v, err := it.Old(i)
		if err != nil {
			return err
		}
		conditions = append(conditions, quote(cols[i])+" IS ?")
		switch v.Type() {
		case sqlite.TypeInteger:
			args = append(args, v.Int64())
		case sqlite.TypeText:
			args = append(args, v.Text())
		case sqlite.TypeBlob:
			args = append(args, v.Blob())
		default:
			return fmt.Errorf("unsupported identity in %s", op.TableName)
		}
	}
	q := "INSERT INTO " + quote(op.TableName) + " SELECT * FROM original." + quote(op.TableName) + " WHERE " + strings.Join(conditions, " AND ")
	if err = sqlitex.ExecuteTransient(c, q, &sqlitex.ExecOptions{Args: args}); err != nil {
		return err
	}
	if c.Changes() != 1 {
		return fmt.Errorf("cannot recover missing %s entity", op.TableName)
	}
	return nil
}

func removeDatabase(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
