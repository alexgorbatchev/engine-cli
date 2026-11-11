package smartlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/google/uuid"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Details struct {
	UUID     string `json:"uuid"`
	Title    string `json:"title"`
	Revision string `json:"revision"`
	ReadOnly bool   `json:"read_only"`
	Reason   string `json:"reason,omitempty"`
	Document
}
type CreateOptions struct {
	Title    string `json:"title"`
	ParentID int64  `json:"parent_id"`
	Document
}
type UpdateOptions struct {
	UUID     string `json:"uuid"`
	Title    string `json:"title"`
	Revision string `json:"revision"`
	Document
}

func failure(code string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("Smartlist operation refused")}
}
func read(ctx context.Context, d db.Reader, id string) (string, string, error) {
	var title string
	var raw sql.NullString
	err := d.QueryRowContext(ctx, `SELECT COALESCE(title,''),rules FROM Smartlist WHERE listUuid=?`, id).Scan(&title, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", failure("smartlist_not_found")
	}
	return title, raw.String, err
}
func Get(ctx context.Context, d *sql.DB, id, zone string) (*Details, error) {
	if _, err := location(zone); err != nil {
		return nil, err
	}
	title, raw, err := read(ctx, d, id)
	if err != nil {
		return nil, err
	}
	result := &Details{UUID: id, Title: title, Revision: revision(title, raw)}
	result.Document, _, err = parse(raw, zone)
	if err != nil {
		result.ReadOnly = true
		result.Reason = "smartlist_rules_read_only"
		result.Document = Document{Rules: []Rule{}, TimeZone: zone}
	}
	return result, nil
}
func title(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalid()
	}
	return value, nil
}
func writeError(err error) error {
	var native *sqlite.Error
	if errors.As(err, &native) && native.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return failure("smartlist_name_conflict")
	}
	return err
}
func Create(ctx context.Context, d *sql.DB, opts CreateOptions) (string, error) {
	name, err := title(opts.Title)
	if err != nil {
		return "", err
	}
	saved, err := encode(opts.Document)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	inventory, err := db.ReadSmartlistInventoryFrom(ctx, tx)
	if err != nil {
		return "", err
	}
	if !inventory.HasSmartlist {
		return "", failure("smartlist_schema_unavailable")
	}
	if len(inventory.Issues) > 0 {
		return "", failure("smartlist_paths_damaged")
	}
	parent := ""
	if opts.ParentID != 0 {
		found := false
		for _, p := range inventory.Playlists {
			if p.ID == opts.ParentID {
				parent = p.Path
				found = true
				break
			}
		}
		if !found {
			return "", failure("smartlist_parent_not_found")
		}
	}
	if err := availableName(ctx, tx, name, parent, ""); err != nil {
		return "", err
	}
	// Inventory normalizes native NULL relationships without rewriting them.
	// Update the terminal by identity so NULL and empty root links behave alike.
	for _, row := range inventory.Saved {
		if row.ParentPath == parent && row.NextPath == "" && row.NextUUID == "" {
			if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET nextListUuid=? WHERE listUuid=?`, id, row.UUID); err != nil {
				return "", err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules,lastEditTime) VALUES(?,?,?,'','',?,DATETIME('now'))`, id, name, parent, string(raw)); err != nil {
		return "", writeError(err)
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}
func Update(ctx context.Context, d *sql.DB, opts UpdateOptions) error {
	name, err := title(opts.Title)
	if err != nil {
		return err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, raw, err := read(ctx, tx, opts.UUID)
	if err != nil {
		return err
	}
	if opts.Revision == "" || opts.Revision != revision(before, raw) {
		return failure("smartlist_changed")
	}
	var parent string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(parentPlaylistPath,'') FROM Smartlist WHERE listUuid=?`, opts.UUID).Scan(&parent); err != nil {
		return err
	}
	if err := availableName(ctx, tx, name, parent, opts.UUID); err != nil {
		return err
	}
	updated, err := updatedRules(raw, opts.Document)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET title=?,rules=? WHERE listUuid=?`, name, updated, opts.UUID); err != nil {
		return writeError(err)
	}
	return tx.Commit()
}

func availableName(ctx context.Context, tx *sql.Tx, name, parent, exclude string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM Smartlist WHERE title=? AND COALESCE(parentPlaylistPath,'')=? AND listUuid<>?)`, name, parent, exclude).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return failure("smartlist_name_conflict")
	}
	return nil
}
