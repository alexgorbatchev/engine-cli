package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/google/uuid"
)

func snapshot(ctx context.Context, d *sql.DB) (map[string]image, error) {
	result := make(map[string]image)
	for _, table := range []string{"Information", "Track", "Playlist", "PlaylistEntity", "Smartlist"} {
		v, err := readTable(ctx, d, table)
		if err != nil {
			return nil, err
		}
		result[table] = v
	}
	return result, nil
}

func readTable(ctx context.Context, d *sql.DB, table string) (result image, err error) {
	rows, err := d.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY rowid")
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	result.Columns, err = rows.Columns()
	if err != nil {
		return result, err
	}
	for rows.Next() {
		values := make([]any, len(result.Columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return result, err
		}
		for i, value := range values {
			if b, ok := value.([]byte); ok {
				values[i] = bytes.Clone(b)
			}
		}
		result.Rows = append(result.Rows, values)
	}
	return result, rows.Err()
}

func verify(ctx context.Context, root string, in inputs, g golden, name, expected string) error {
	if err := absentDesktop(ctx); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.Inputs.SourceCommit != in.SourceCommit || m.Inputs.Golden != in.Golden {
		return errors.New("seed provenance changed")
	}
	if err := validateFixtures(m.Fixtures); err != nil {
		return err
	}
	if expected != "seed" && expected != "repaired" && expected != "repair-undone" && expected != "original-deletion-undone" {
		return errors.New("unknown expected state")
	}
	found := false
	for _, f := range m.Fixtures {
		if name != "" && f.Name != name {
			continue
		}
		found = true
		if err := verifyOne(ctx, root, g, f, expected); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	if !found {
		return errors.New("no matching fixture")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"verified": true, "expected": expected, "name": name, "desktop_evaluation": "NOT_PROVEN"})
}

func validateFixtures(fixtures []fixture) error {
	if len(fixtures) != 4 {
		return errors.New("manifest must retain all four synthetic cases")
	}
	seen := make(map[string]bool)
	identities := make(map[string]bool)
	for _, f := range fixtures {
		switch f.Name {
		case "healthy", "reconnect", "unknown-removal", "original-deletion":
		default:
			return errors.New("unknown synthetic case")
		}
		if seen[f.Name] {
			return errors.New("duplicate synthetic case")
		}
		seen[f.Name] = true
		if _, err := uuid.Parse(f.UUID); err != nil || identities[f.UUID] {
			return errors.New("invalid or duplicate fixture library identity")
		}
		identities[f.UUID] = true
	}
	return nil
}

func verifyOne(ctx context.Context, root string, g golden, f fixture, expected string) (err error) {
	if f.Name != "healthy" && f.Name != "reconnect" && f.Name != "unknown-removal" && f.Name != "original-deletion" {
		return errors.New("invalid fixture name")
	}
	lib := filepath.Join(root, f.Name)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"), true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	var identity string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information").Scan(&identity); err != nil {
		return err
	}
	if identity != f.UUID {
		return errors.New("fixture library identity changed")
	}
	if err := verifyMedia(ctx, lib, d, g); err != nil {
		return err
	}
	actual, err := snapshot(ctx, d)
	if err != nil {
		return err
	}
	want, err := expectedRows(f, expected)
	if err != nil {
		return err
	}
	if err := sameJSON(want, actual); err != nil {
		return fmt.Errorf("complete native rows changed beyond expected transition: %w", err)
	}
	if f.EntryID != "" && (f.Name == "reconnect" || expected == "seed") {
		entry, err := os.ReadFile(filepath.Join(lib, ".engine-cli", "history", f.EntryID+".json"))
		if err != nil {
			return err
		}
		if digest(entry) != f.EntrySHA256 {
			return errors.New("original operation entry changed")
		}
	}
	review, err := history.PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		return err
	}
	if expected == "seed" {
		return sameJSON(f.Review, review)
	}
	if expected == "repair-undone" {
		// Undo returns the precise stale saved-row baseline, not a healthy library.
		if review.Reconnected != f.Review.Reconnected || review.Removed != f.Review.Removed || review.AmendedDeletes != f.Review.AmendedDeletes {
			return errors.New("repair Undo changed the original repairable component")
		}
		return sameJSON(f.Review.Rows, review.Rows)
	}
	if len(review.Rows) != 0 {
		return errors.New("unexpected repair remains after intended transition")
	}
	return nil
}

func expectedRows(f fixture, expected string) (map[string]image, error) {
	// Decode a fresh copy: transformations must not mutate the recorded original.
	data, err := json.Marshal(f.Original)
	if err != nil {
		return nil, err
	}
	var result map[string]image
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if expected == "seed" || f.Name == "healthy" {
		return result, nil
	}
	if expected == "original-deletion-undone" {
		if f.Name != "original-deletion" {
			return nil, errors.New("original deletion Undo requires that case alone")
		}
		return f.BeforeOperation, nil
	}
	if expected == "repair-undone" && f.Name != "reconnect" {
		return nil, errors.New("repair Undo belongs only to reconnect")
	}
	if expected == "repair-undone" {
		return result, nil
	}
	saved := result["Smartlist"]
	uuidIndex, parentIndex, nextIndex := -1, -1, -1
	for i, name := range saved.Columns {
		if name == "listUuid" {
			uuidIndex = i
		}
		if name == "parentPlaylistPath" {
			parentIndex = i
		}
		if name == "nextPlaylistPath" {
			nextIndex = i
		}
	}
	if uuidIndex < 0 || parentIndex < 0 || nextIndex < 0 {
		return nil, errors.New("missing saved-list identity/order columns")
	}
	var rows [][]any
	for _, row := range saved.Rows {
		if row[uuidIndex] == targetID || row[uuidIndex] == targetTailID {
			if f.Name != "reconnect" {
				continue
			}
			row[parentIndex] = "Renamed;"
			if row[uuidIndex] == targetTailID {
				row[nextIndex] = "Renamed;Leaf;"
			}
		}
		rows = append(rows, row)
	}
	saved.Rows = rows
	result["Smartlist"] = saved
	return result, nil
}

func sameJSON(a, b any) error {
	left, err := json.Marshal(a)
	if err != nil {
		return err
	}
	right, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var x, y any
	if err := json.Unmarshal(left, &x); err != nil {
		return err
	}
	if err := json.Unmarshal(right, &y); err != nil {
		return err
	}
	if !reflect.DeepEqual(x, y) {
		return errors.New("values differ")
	}
	return nil
}
