// Package state stores machine-local library knowledge and cosmetic UI preferences.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Library struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Hidden  bool   `json:"hidden"`
	Retired bool   `json:"retired"`
}

type Store struct{ database *sql.DB }

const applicationID = 0x454E4753
const schemaVersion = 1

var ErrInvalidPreference = errors.New("invalid library preference")

func DefaultPath() (string, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(root) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(root, "engine-cli", "state.db"), nil
}

// An empty path provides an isolated in-memory store for embedded callers.
func Open(path string) (*Store, error) {
	dsn := ":memory:"
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
			return nil, err
		}
		uri := filepath.ToSlash(abs)
		if !strings.HasPrefix(uri, "/") {
			uri = "/" + uri
		}
		dsn = (&url.URL{Scheme: "file", Path: uri}).String() + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	}
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	tx, err := d.Begin()
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	var application, version, count int
	if err = tx.QueryRow("SELECT application_id,user_version,(SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%') FROM pragma_application_id,pragma_user_version").Scan(&application, &version, &count); err != nil {
		return nil, errors.Join(err, tx.Rollback(), d.Close())
	}
	if (application != 0 && application != applicationID) || (application == 0 && (count > 0 || version != 0)) || (application == applicationID && version != schemaVersion) {
		return nil, errors.Join(fmt.Errorf("not a supported Engine state database"), tx.Rollback(), d.Close())
	}
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS libraries(path TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', hidden INTEGER NOT NULL DEFAULT 0 CHECK(hidden IN (0,1)), retired INTEGER NOT NULL DEFAULT 0 CHECK(retired IN (0,1))) STRICT`)
	if err != nil {
		return nil, fmt.Errorf("initialize state database: %w", errors.Join(err, tx.Rollback(), d.Close()))
	}
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", applicationID, schemaVersion)); err != nil {
		return nil, errors.Join(err, tx.Rollback(), d.Close())
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	return &Store{database: d}, nil
}

func (s *Store) Close() error { return s.database.Close() }

func (s *Store) Remember(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("library state requires an absolute path")
	}
	_, err := s.database.ExecContext(ctx, "INSERT INTO libraries(path) VALUES(?) ON CONFLICT(path) DO UPDATE SET retired=0", filepath.Clean(path))
	return err
}

func (s *Store) Update(ctx context.Context, path string, name *string, hidden *bool) error {
	if name != nil && (strings.TrimSpace(*name) == "" || len(*name) > 200) {
		return fmt.Errorf("%w: name must contain 1 to 200 characters", ErrInvalidPreference)
	}
	if err := s.Remember(ctx, path); err != nil {
		return err
	}
	_, err := s.database.ExecContext(ctx, "UPDATE libraries SET name=COALESCE(?,name),hidden=COALESCE(?,hidden) WHERE path=?", name, hidden, filepath.Clean(path))
	return err
}

func (s *Store) List(ctx context.Context) ([]Library, error) {
	rows, err := s.database.QueryContext(ctx, "SELECT path,name,hidden,retired FROM libraries ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Library{}
	for rows.Next() {
		var lib Library
		if err = rows.Scan(&lib.Path, &lib.Name, &lib.Hidden, &lib.Retired); err != nil {
			return nil, err
		}
		result = append(result, lib)
	}
	return result, rows.Err()
}

func (s *Store) Retire(ctx context.Context, path string) error {
	_, err := s.database.ExecContext(ctx, "UPDATE libraries SET hidden=1,retired=1 WHERE path=?", path)
	return err
}
