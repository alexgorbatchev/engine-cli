package fixturecapture

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func snapshotImage(ctx context.Context, archive, working, snapshots string) (err error) {
	root, err := watchfs.Open(archive)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err := os.MkdirAll(working, 0o700); err != nil {
		return err
	}
	image, err := inventory(ctx, root, working)
	if err != nil {
		return err
	}
	files := map[string]bool{}
	for _, file := range image.Files {
		files[file.Path] = true
	}
	for path := range files {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if strings.HasSuffix(path, ".db"+suffix) && !files[strings.TrimSuffix(path, suffix)] {
				return invalidInput("orphan database sidecar: %q", path)
			}
		}
		if strings.HasSuffix(path, ".db-shm") && !files[strings.TrimSuffix(path, "-shm")+"-wal"] {
			return invalidInput("shared-memory sidecar without WAL: %q", path)
		}
		if strings.HasSuffix(path, ".db-wal") && files[strings.TrimSuffix(path, "-wal")+"-journal"] {
			return invalidInput("ambiguous WAL and rollback journal: %q", path)
		}
	}
	count := 0
	for _, file := range image.Files {
		if !strings.HasSuffix(file.Path, ".db") {
			continue
		}
		count++
		destination := filepath.Join(snapshots, file.Path)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err := recoverSnapshot(ctx, filepath.Join(working, file.Path), destination); err != nil {
			return fmt.Errorf("snapshot %q: %w", file.Path, err)
		}
		if err := exportDatabase(ctx, destination, destination+".json"); err != nil {
			return err
		}
	}
	if count == 0 {
		return fmt.Errorf("offline input contains no database files: %w", fs.ErrNotExist)
	}
	return nil
}

func sqliteURI(path, mode string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {mode}}.Encode()}
	return u.String()
}

// Only the working copy is opened writable. SQLite can recover its copied hot
// journal or WAL here; the archival family is never opened by SQLite.
func recoverSnapshot(ctx context.Context, working, destination string) (err error) {
	database, err := sql.Open("sqlite", sqliteURI(working, "rw"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	var result string
	if err := database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return invalidInput("working database integrity check refused: %q", result)
	}
	_, err = backup.SnapshotDatabase(ctx, working, destination)
	return err
}

// Value retains SQL storage types, NULLs and exact TEXT/BLOB bytes. JSON strings
// alone would silently replace invalid UTF-8 in Desktop-written rule payloads.
type Value struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

type Table struct {
	Name    string    `json:"name"`
	SQL     string    `json:"sql"`
	Columns []string  `json:"columns"`
	Rows    [][]Value `json:"rows"`
}

func exportDatabase(ctx context.Context, path, output string) (err error) {
	database, err := sql.Open("sqlite", sqliteURI(path, "ro"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	rows, err := database.QueryContext(ctx, "SELECT name, sql FROM sqlite_schema WHERE type = 'table' ORDER BY name")
	if err != nil {
		return err
	}
	tables := []Table{}
	for rows.Next() {
		var table Table
		if err := rows.Scan(&table.Name, &table.SQL); err != nil {
			return errors.Join(err, rows.Close())
		}
		if !utf8.ValidString(table.Name) || !utf8.ValidString(table.SQL) {
			return errors.Join(invalidInput("unsupported invalid UTF-8 schema metadata"), rows.Close())
		}
		tables = append(tables, table)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for index := range tables {
		if err := exportTable(ctx, database, &tables[index]); err != nil {
			return err
		}
	}
	return writeJSON(output, tables)
}

func exportTable(ctx context.Context, database *sql.DB, table *Table) error {
	rows, err := database.QueryContext(ctx, "SELECT * FROM \""+strings.ReplaceAll(table.Name, "\"", "\"\"")+"\"")
	if err != nil {
		return err
	}
	table.Columns, err = rows.Columns()
	if err != nil {
		return errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// CASE removes declared-type conversion (notably DATETIME), while BLOB
	// casts preserve exact TEXT bytes. typeof records the original SQL type.
	var expressions []string
	for _, column := range table.Columns {
		if !utf8.ValidString(column) {
			return invalidInput("unsupported invalid UTF-8 column metadata")
		}
		quoted := "\"" + strings.ReplaceAll(column, "\"", "\"\"") + "\""
		expressions = append(expressions, "typeof("+quoted+")", "CASE WHEN typeof("+quoted+") IN ('text','blob') THEN CAST("+quoted+" AS BLOB) ELSE "+quoted+" END")
	}
	rows, err = database.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM \""+strings.ReplaceAll(table.Name, "\"", "\"\"")+"\"")
	if err != nil {
		return err
	}
	table.Rows = [][]Value{}
	for rows.Next() {
		values := make([]any, len(table.Columns)*2)
		targets := make([]any, len(values))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return errors.Join(err, rows.Close())
		}
		record := make([]Value, len(table.Columns))
		for i := range record {
			kind, ok := values[i*2].(string)
			if !ok {
				return errors.Join(fmt.Errorf("missing SQLite storage type"), rows.Close())
			}
			value := values[i*2+1]
			switch v := value.(type) {
			case nil:
				record[i] = Value{Type: "null"}
			case string:
				record[i] = Value{Type: "text", Data: base64.StdEncoding.EncodeToString([]byte(v))}
			case []byte:
				record[i] = Value{Type: kind, Data: base64.StdEncoding.EncodeToString(v)}
			case int64:
				record[i] = Value{Type: "integer", Data: strconv.FormatInt(v, 10)}
			case float64:
				record[i] = Value{Type: "real", Data: strconv.FormatFloat(v, 'g', -1, 64)}
			default:
				return errors.Join(fmt.Errorf("unsupported SQLite value type %T", value), rows.Close())
			}
		}
		table.Rows = append(table.Rows, record)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	return nil
}
