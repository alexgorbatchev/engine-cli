package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// RemoveSmartlist follows Desktop's park/relink/delete transaction. Rules remain
// opaque, and ordinary playlists and their memberships are never written.
func RemoveSmartlist(ctx context.Context, database *sql.DB, uuid string) (int64, error) {
	if strings.TrimSpace(uuid) == "" {
		return 0, &progress.Error{Code: "invalid_parameters", Cause: fmt.Errorf("Smartlist UUID is required")}
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	inventory, err := db.ReadSmartlistInventoryFrom(ctx, tx)
	if err != nil {
		return 0, err
	}
	var target *db.SmartlistRecord
	for i := range inventory.Saved {
		if inventory.Saved[i].UUID == uuid {
			target = &inventory.Saved[i]
			break
		}
	}
	if target == nil {
		return 0, &progress.Error{Code: "smartlist_not_found", Cause: fmt.Errorf("Smartlist no longer exists")}
	}
	if len(inventory.Issues) > 0 {
		return 0, &progress.Error{Code: "playlist_order_invalid", Cause: fmt.Errorf("Smartlist links need repair")}
	}
	var parent int64
	for _, p := range inventory.Playlists {
		if p.Path == target.ParentPath {
			parent = p.ID
			break
		}
	}
	// Parking first releases the native unique (parent,next) slot before its
	// predecessor takes it. Preserve nullable native successor values verbatim.
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET parentPlaylistPath=listUuid WHERE listUuid=?`, uuid); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist AS target SET nextPlaylistPath=removed.nextPlaylistPath,nextListUuid=removed.nextListUuid FROM (SELECT listUuid,nextPlaylistPath,nextListUuid FROM Smartlist WHERE listUuid=?) AS removed WHERE target.nextListUuid=removed.listUuid`, uuid); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM Smartlist WHERE listUuid=?`, uuid); err != nil {
		return 0, err
	}
	return parent, tx.Commit()
}
