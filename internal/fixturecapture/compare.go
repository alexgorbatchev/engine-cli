package fixturecapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// Change compares captured native rows, not an evaluation of their rules.
// The complete typed rows retain NULL/empty distinctions and every link field.
type Change struct {
	Database string   `json:"database"`
	Table    string   `json:"table"`
	Key      Value    `json:"key"`
	Columns  []string `json:"columns"`
	Before   []Value  `json:"before"`
	After    []Value  `json:"after"`
}

func compareImages(output string, before, after Image) error {
	paths := map[string]bool{}
	for _, image := range []Image{before, after} {
		for _, file := range image.Files {
			if filepath.Ext(file.Path) == ".db" {
				paths[file.Path] = true
			}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := []Change{}
	for _, path := range ordered {
		a, err := readTables(filepath.Join(output, "snapshots", "before", path+".json"))
		if err != nil {
			return err
		}
		b, err := readTables(filepath.Join(output, "snapshots", "after", path+".json"))
		if err != nil {
			return err
		}
		for _, image := range []struct {
			image  Image
			tables []Table
		}{{before, a}, {after, b}} {
			for _, file := range image.image.Files {
				if file.Path == path && image.tables == nil {
					return fmt.Errorf("missing export for captured database %q", path)
				}
			}
		}
		for _, name := range []string{"Smartlist", "Playlist", "PlaylistEntity"} {
			left, right := findTable(a, name), findTable(b, name)
			if left == nil && right == nil {
				continue
			}
			var columns []string
			if left != nil {
				columns = left.Columns
			}
			if right != nil {
				if left != nil && !reflect.DeepEqual(columns, right.Columns) {
					return fmt.Errorf("comparison requires matching columns for %q in %q; inspect both raw exports", name, path)
				}
				columns = right.Columns
			}
			keyIndex := -1
			for i, column := range columns {
				if column == "listUuid" || column == "id" {
					keyIndex = i
					break
				}
			}
			if keyIndex < 0 {
				return fmt.Errorf("comparison missing native row identity in %q", name)
			}
			leftRows, err := keyRows(left, keyIndex)
			if err != nil {
				return err
			}
			rightRows, err := keyRows(right, keyIndex)
			if err != nil {
				return err
			}
			keys := map[string]bool{}
			for key := range leftRows {
				keys[key] = true
			}
			for key := range rightRows {
				keys[key] = true
			}
			orderedKeys := make([]string, 0, len(keys))
			for key := range keys {
				orderedKeys = append(orderedKeys, key)
			}
			sort.Strings(orderedKeys)
			for _, key := range orderedKeys {
				old, new := leftRows[key], rightRows[key]
				if reflect.DeepEqual(old, new) {
					continue
				}
				row := old
				if row == nil {
					row = new
				}
				changes = append(changes, Change{Database: path, Table: name, Key: row[keyIndex], Columns: columns, Before: old, After: new})
			}
		}
	}
	return writeJSON(filepath.Join(output, "row-changes.json"), changes)
}

func readTables(path string) ([]Table, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tables []Table
	err = json.Unmarshal(data, &tables)
	return tables, err
}

func findTable(tables []Table, name string) *Table {
	for i := range tables {
		if tables[i].Name == name {
			return &tables[i]
		}
	}
	return nil
}

func keyRows(table *Table, keyIndex int) (map[string][]Value, error) {
	rows := map[string][]Value{}
	if table == nil {
		return rows, nil
	}
	for _, row := range table.Rows {
		if keyIndex >= len(row) || row[keyIndex].Type == "null" {
			return nil, fmt.Errorf("invalid native row identity in %q", table.Name)
		}
		key, err := json.Marshal(row[keyIndex])
		if err != nil {
			return nil, err
		}
		if _, exists := rows[string(key)]; exists {
			return nil, fmt.Errorf("duplicate native row identity in %q", table.Name)
		}
		rows[string(key)] = row
	}
	return rows, nil
}
