package rekordbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const targetCode = "rekordbox_import_target"

// importTarget is where the new root goes: under parentID (0 for the library's
// top level), named name, or the first free "name (N)" when unique is set.
type importTarget struct {
	name     string
	parentID int64
	unique   bool
}

// destinationIdentity is checked during review and again in the write
// transaction; an existing root is never treated as an append destination. It
// returns the library identity and the exact name the new root takes.
func destinationIdentity(ctx context.Context, d db.Executor, target importTarget) (string, string, error) {
	var uuid string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return "", "", fmt.Errorf("reading import destination identity: %w", err)
	}
	if uuid == "" {
		return "", "", importError(targetCode, "destination library has no native identity")
	}
	if target.parentID != 0 {
		var present int64
		err := d.QueryRowContext(ctx, "SELECT id FROM Playlist WHERE id=?", target.parentID).Scan(&present)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", importError(targetCode, "the parent playlist no longer exists")
		}
		if err != nil {
			return "", "", fmt.Errorf("checking import parent: %w", err)
		}
	}
	name, err := db.FreePlaylistName(ctx, d, target.parentID, target.name)
	if err != nil {
		return "", "", err
	}
	if name != target.name && !target.unique {
		return "", "", importError(targetCode, "choose an unused name for the new import root")
	}
	return uuid, name, nil
}

// resolveParent turns an opaque playlist reference into its numeric ID before
// the review reads; numeric IDs are checked against the library by the review.
func resolveParent(ctx context.Context, d *sql.DB, parent string) (int64, error) {
	if parent == "" || parent == "0" {
		return 0, nil
	}
	// Titles are display fields, never selectors: only IDs and references name a parent.
	if !strings.HasPrefix(parent, "eng:") {
		id, err := strconv.ParseInt(parent, 10, 64)
		if err != nil || id <= 0 {
			return 0, importError(targetCode, "invalid parent playlist")
		}
		return id, nil
	}
	found, err := db.FindPlaylist(ctx, d, parent)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	// Malformed, foreign, and ambiguous references all fail to name a parent.
	if err != nil || found == nil {
		return 0, importError(targetCode, "the parent playlist no longer exists")
	}
	return found.ID, nil
}
