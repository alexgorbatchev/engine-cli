package identity

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

// LibraryPath obtains the selected database location without relying on a UI's
// previously cached mount path.
func LibraryPath(ctx context.Context, d *sql.DB) (string, error) {
	rows, err := d.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return "", err
		}
		if name == "main" && path != "" {
			dir := filepath.Dir(path)
			if filepath.Base(dir) == "Database2" {
				dir = filepath.Dir(dir)
			}
			return dir, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("database has no persistent library location")
}

func ResolveDatabase(ctx context.Context, d *sql.DB, kind, ref string) (int64, error) {
	lib, err := LibraryPath(ctx, d)
	if err != nil {
		return 0, err
	}
	return Resolve(ctx, d, lib, kind, ref)
}
