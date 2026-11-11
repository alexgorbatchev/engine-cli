package playlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// RenameSmartlist preserves opaque rules and native links, including versions
// this application cannot edit. Desktop's rename job changes only the title.
func RenameSmartlist(ctx context.Context, database *sql.DB, uuid, title string) error {
	title = strings.TrimSpace(title)
	if strings.TrimSpace(uuid) == "" || title == "" {
		return &progress.Error{Code: "invalid_parameters", Cause: fmt.Errorf("Smartlist UUID and title are required")}
	}
	result, err := database.ExecContext(ctx, `UPDATE Smartlist SET title=? WHERE listUuid=?`, title, uuid)
	if err != nil {
		var native *sqlite.Error
		if errors.As(err, &native) && native.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return &progress.Error{Code: "smartlist_name_conflict", Cause: err}
		}
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return &progress.Error{Code: "smartlist_not_found", Cause: fmt.Errorf("Smartlist no longer exists")}
	}
	return nil
}
