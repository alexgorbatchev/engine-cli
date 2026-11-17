package history

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// exclusion keeps one row, or one column of it, as it is now when an undo is
// applied: the part of a reviewed undo the person chose not to undo.
type exclusion struct {
	table string
	key   []any
	field string
}

// selector names a reviewed row as "<table>:<key>", the form review changes and
// conflicts report and an undo's exclude list accepts; "#<field>" narrows it to
// one column.
func selector(table string, key []any) string {
	return table + ":" + keyIdentity(key)
}

// parseExclusions reads exclude selectors, refusing anything that does not name
// a table and a primary key.
func parseExclusions(selectors []string) ([]exclusion, error) {
	result := make([]exclusion, 0, len(selectors))
	for _, raw := range selectors {
		body, field, _ := strings.Cut(raw, "#")
		table, encoded, ok := strings.Cut(body, ":")
		if !ok || table == "" || encoded == "" {
			return nil, fmt.Errorf("invalid exclude selector %q", raw)
		}
		var parts []string
		if err := json.Unmarshal([]byte(encoded), &parts); err != nil || len(parts) == 0 {
			return nil, fmt.Errorf("invalid exclude selector %q", raw)
		}
		key := make([]any, len(parts))
		for i, part := range parts {
			kind, value, _ := strings.Cut(part, ":")
			switch kind {
			case sqlite.TypeInteger.String():
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid exclude selector %q", raw)
				}
				key[i] = n
			case sqlite.TypeFloat.String():
				f, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid exclude selector %q", raw)
				}
				key[i] = f
			case sqlite.TypeText.String():
				text, err := hex.DecodeString(value)
				if err != nil {
					return nil, fmt.Errorf("invalid exclude selector %q", raw)
				}
				key[i] = string(text)
			case sqlite.TypeBlob.String():
				blob, err := hex.DecodeString(value)
				if err != nil {
					return nil, fmt.Errorf("invalid exclude selector %q", raw)
				}
				key[i] = blob
			default:
				return nil, fmt.Errorf("invalid exclude selector %q", raw)
			}
		}
		result = append(result, exclusion{table: table, key: key, field: field})
	}
	return result, nil
}

// changeKey reads the primary key of the change an iterator points at.
func changeKey(it *sqlite.ChangesetIterator, op *sqlite.ChangesetOperation) ([]any, error) {
	pk, err := it.PrimaryKey()
	if err != nil {
		return nil, err
	}
	var key []any
	for i, isKey := range pk {
		if !isKey {
			continue
		}
		v, err := it.Old(i)
		if op.Type == sqlite.OpInsert {
			v, err = it.New(i)
		}
		if err != nil {
			return nil, err
		}
		key = append(key, keyValue(v))
	}
	return key, nil
}

func bindKey(args []any, key []any) []any {
	for _, value := range key {
		args = append(args, value)
	}
	return args
}

// keepExcluded puts excluded rows and columns of a restoration image back to
// their current content, read from the database at current.
func keepExcluded(c *sqlite.Conn, current string, exclusions []exclusion) error {
	if err := sqlitex.ExecuteTransient(c, "ATTACH ? AS live", &sqlitex.ExecOptions{Args: []any{current}}); err != nil {
		return err
	}
	defer sqlitex.ExecuteTransient(c, "DETACH live", nil)
	for _, ex := range exclusions {
		tables, err := queryStrings(c, "SELECT name FROM sqlite_schema WHERE type='table' AND name=?", ex.table)
		if err != nil {
			return err
		}
		if len(tables) == 0 {
			continue
		}
		keyCols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) WHERE pk>0 ORDER BY pk", ex.table)
		if err != nil {
			return err
		}
		if len(keyCols) != len(ex.key) {
			return fmt.Errorf("invalid exclude selector for %s", ex.table)
		}
		conditions := make([]string, len(keyCols))
		for i, col := range keyCols {
			conditions[i] = quote(col) + " IS ?"
		}
		where := strings.Join(conditions, " AND ")
		table := quote(ex.table)
		if ex.field != "" {
			cols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) WHERE name=?", ex.table, ex.field)
			if err != nil {
				return err
			}
			if len(cols) == 0 {
				return fmt.Errorf("invalid exclude field %s.%s", ex.table, ex.field)
			}
			col := quote(ex.field)
			q := "UPDATE main." + table + " SET " + col + "=(SELECT " + col + " FROM live." + table + " WHERE " + where + ") WHERE " + where + " AND EXISTS(SELECT 1 FROM live." + table + " WHERE " + where + ")"
			if err := sqlitex.ExecuteTransient(c, q, &sqlitex.ExecOptions{Args: bindKey(bindKey(bindKey(nil, ex.key), ex.key), ex.key)}); err != nil {
				return err
			}
			continue
		}
		if err := sqlitex.ExecuteTransient(c, "DELETE FROM main."+table+" WHERE "+where, &sqlitex.ExecOptions{Args: bindKey(nil, ex.key)}); err != nil {
			return err
		}
		if err := sqlitex.ExecuteTransient(c, "INSERT INTO main."+table+" SELECT * FROM live."+table+" WHERE "+where, &sqlitex.ExecOptions{Args: bindKey(nil, ex.key)}); err != nil {
			return err
		}
	}
	return nil
}

// ValidateExclusions checks exclude selectors before an undo starts.
func ValidateExclusions(selectors []string) error {
	_, err := parseExclusions(selectors)
	return err
}
