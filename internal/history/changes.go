package history

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"zombiezen.com/go/sqlite"
)

// FieldChange is one column a change moves from one value to another. Binary
// columns, such as beat grids and cues, report the field without values.
type FieldChange struct {
	Field  string `json:"field"`
	From   any    `json:"from"`
	To     any    `json:"to"`
	Binary bool   `json:"binary,omitempty"`
}

// bookkeepingColumns change with every edit and say nothing about what changed.
var bookkeepingColumns = map[string]map[string]bool{
	"Track":          {"lastEditTime": true, "isMetadataOfPackedTrackChanged": true, "isPerfomanceDataOfPackedTrackChanged": true, "albumArtSourceHash": true},
	"Playlist":       {"lastEditTime": true, "nextListId": true},
	"PlaylistEntity": {"nextEntityId": true, "membershipReference": true},
	"Smartlist":      {"lastEditTime": true, "nextListUuid": true, "nextPlaylistPath": true},
}

// rowChange is one row a changeset inserts, deletes or updates. values holds the
// row as it is where it exists (after an insert, before a delete, after an
// update); fields lists an update's changed columns.
type rowChange struct {
	table  string
	op     sqlite.OpType
	key    []any
	values map[string]any
	fields []FieldChange
}

// rowID is the row's integer primary key, or zero for other keys.
func (r rowChange) rowID() int64 {
	if len(r.key) == 1 {
		if id, ok := r.key[0].(int64); ok {
			return id
		}
	}
	return 0
}

func (r rowChange) text(column string) string {
	if value, ok := r.values[column].(string); ok {
		return value
	}
	return ""
}

func (r rowChange) integer(column string) int64 {
	if value, ok := r.values[column].(int64); ok {
		return value
	}
	return 0
}

// cell reads one result column; blobs report only that they are binary.
func cell(s *sqlite.Stmt, i int) (value any, binary bool, err error) {
	switch s.ColumnType(i) {
	case sqlite.TypeInteger:
		return s.ColumnInt64(i), false, nil
	case sqlite.TypeFloat:
		return s.ColumnFloat(i), false, nil
	case sqlite.TypeText:
		return s.ColumnText(i), false, nil
	case sqlite.TypeBlob:
		data, err := io.ReadAll(s.ColumnReader(i))
		return hex.EncodeToString(data), true, err
	default:
		return nil, false, nil
	}
}

func keyValue(v sqlite.Value) any {
	switch v.Type() {
	case sqlite.TypeInteger:
		return v.Int64()
	case sqlite.TypeFloat:
		return v.Float()
	case sqlite.TypeText:
		return v.Text()
	case sqlite.TypeBlob:
		return v.Blob()
	default:
		return nil
	}
}

// readRow reads one row by primary key from schema ("main" or "previous"),
// returning nil when it does not exist there.
func readRow(c *sqlite.Conn, schema, table string, cols []string, keyCols []string, key []any) (values map[string]any, binary map[string]bool, err error) {
	conditions := make([]string, len(keyCols))
	for i, col := range keyCols {
		conditions[i] = quote(col) + " IS ?"
	}
	stmt, _, err := c.PrepareTransient("SELECT * FROM " + schema + "." + quote(table) + " WHERE " + strings.Join(conditions, " AND "))
	if err != nil {
		return nil, nil, err
	}
	defer stmt.Finalize()
	for i, value := range key {
		switch v := value.(type) {
		case int64:
			stmt.BindInt64(i+1, v)
		case float64:
			stmt.BindFloat(i+1, v)
		case string:
			stmt.BindText(i+1, v)
		case []byte:
			stmt.BindBytes(i+1, v)
		default:
			stmt.BindNull(i + 1)
		}
	}
	has, err := stmt.Step()
	if err != nil || !has {
		return nil, nil, err
	}
	values, binary = make(map[string]any, len(cols)), make(map[string]bool)
	for i := 0; i < stmt.ColumnCount() && i < len(cols); i++ {
		value, isBinary, err := cell(stmt, i)
		if err != nil {
			return nil, nil, err
		}
		values[cols[i]] = value
		if isBinary {
			binary[cols[i]] = true
		}
	}
	return values, binary, nil
}

// rowChanges lists the row changes that turn the database at from into the one
// at to, reading both versions of every changed row. It returns nil when the
// change is database-wide, as changeset does.
func rowChanges(ctx context.Context, from, to string) ([]rowChange, error) {
	delta, err := changeset(ctx, from, to)
	if err != nil || len(delta) == 0 {
		return nil, err
	}
	c, err := open(ctx, to, sqlite.OpenReadOnly)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err = execAttach(c, from); err != nil {
		return nil, err
	}
	columns := make(map[string][]string)
	keyColumns := make(map[string][]string)
	it, err := sqlite.NewChangesetIterator(bytes.NewReader(delta))
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var result []rowChange
	for {
		more, err := it.Next()
		if err != nil {
			return nil, err
		}
		if !more {
			return result, nil
		}
		op, err := it.Operation()
		if err != nil {
			return nil, err
		}
		cols, ok := columns[op.TableName]
		if !ok {
			if cols, err = queryStrings(c, "SELECT name FROM pragma_table_info(?) ORDER BY cid", op.TableName); err != nil {
				return nil, err
			}
			columns[op.TableName] = cols
		}
		pk, err := it.PrimaryKey()
		if err != nil {
			return nil, err
		}
		if _, ok := keyColumns[op.TableName]; !ok {
			var keys []string
			for i, isKey := range pk {
				if isKey && i < len(cols) {
					keys = append(keys, cols[i])
				}
			}
			keyColumns[op.TableName] = keys
		}
		change := rowChange{table: op.TableName, op: op.Type}
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
			change.key = append(change.key, keyValue(v))
		}
		before, beforeBinary, err := readRow(c, "previous", op.TableName, cols, keyColumns[op.TableName], change.key)
		if err != nil {
			return nil, err
		}
		after, afterBinary, err := readRow(c, "main", op.TableName, cols, keyColumns[op.TableName], change.key)
		if err != nil {
			return nil, err
		}
		switch op.Type {
		case sqlite.OpInsert:
			change.values = after
		case sqlite.OpDelete:
			change.values = before
		default:
			change.values = after
			for _, col := range cols {
				if bookkeepingColumns[op.TableName][col] || sameCell(before[col], after[col]) {
					continue
				}
				field := FieldChange{Field: col, From: before[col], To: after[col]}
				if beforeBinary[col] || afterBinary[col] {
					field = FieldChange{Field: col, Binary: true}
				}
				change.fields = append(change.fields, field)
			}
			if len(change.fields) == 0 {
				continue
			}
		}
		if change.values == nil {
			change.values = map[string]any{}
		}
		result = append(result, change)
	}
}

func execAttach(c *sqlite.Conn, path string) error {
	stmt, _, err := c.PrepareTransient("ATTACH ? AS previous")
	if err != nil {
		return err
	}
	defer stmt.Finalize()
	stmt.BindText(1, path)
	_, err = stmt.Step()
	return err
}

func sameCell(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	}
	return false
}

// keyIdentity encodes a primary key the way collectEntities names entities.
func keyIdentity(key []any) string {
	parts := make([]string, 0, len(key))
	for _, value := range key {
		switch v := value.(type) {
		case int64:
			parts = append(parts, sqlite.TypeInteger.String()+":"+strconv.FormatInt(v, 10))
		case float64:
			parts = append(parts, sqlite.TypeFloat.String()+":"+strconv.FormatFloat(v, 'g', -1, 64))
		case string:
			parts = append(parts, sqlite.TypeText.String()+":"+hex.EncodeToString([]byte(v)))
		case []byte:
			parts = append(parts, sqlite.TypeBlob.String()+":"+hex.EncodeToString(v))
		default:
			parts = append(parts, sqlite.TypeNull.String()+":")
		}
	}
	data, _ := json.Marshal(parts)
	return string(data)
}

// titles reads one text column for the given row IDs from each database in
// turn, keeping the first non-empty value, so removed and added rows are named.
func titles(ctx context.Context, table, column string, ids []int64, databases ...string) (map[int64]string, error) {
	result := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	for _, path := range databases {
		if path == "" {
			continue
		}
		if err := readTitles(ctx, path, table, column, ids, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func readTitles(ctx context.Context, path, table, column string, ids []int64, result map[int64]string) error {
	c, err := open(ctx, path, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	defer c.Close()
	cols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) WHERE name=?", table, column)
	if err != nil || len(cols) == 0 {
		return err
	}
	stmt, _, err := c.PrepareTransient("SELECT COALESCE(" + quote(column) + ",'') FROM " + quote(table) + " WHERE id=?")
	if err != nil {
		return err
	}
	defer stmt.Finalize()
	for _, id := range ids {
		if result[id] != "" {
			continue
		}
		stmt.BindInt64(1, id)
		has, err := stmt.Step()
		if err != nil {
			return err
		}
		if has {
			result[id] = stmt.ColumnText(0)
		}
		if err := stmt.Reset(); err != nil {
			return err
		}
	}
	return nil
}
