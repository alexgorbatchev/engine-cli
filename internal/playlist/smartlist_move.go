package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type smartlistPosition struct {
	parentID   int64
	parentPath string
	nextPath   string
	nextUUID   string
}

func moveRefusal(code, message string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("%s", message)}
}

func smartlistMovePosition(inventory *db.SmartlistInventory, source db.SmartlistRecord, to, before string) (smartlistPosition, error) {
	var position smartlistPosition
	if to != "" && to != "0" {
		id, err := strconv.ParseInt(to, 10, 64)
		if err != nil || id <= 0 {
			return position, moveRefusal("smartlist_move_parent", "destination must be an ordinary playlist")
		}
		for _, p := range inventory.Playlists {
			if p.ID == id {
				position.parentID, position.parentPath = id, p.Path
				break
			}
		}
		if position.parentID == 0 {
			return position, moveRefusal("smartlist_move_parent", "destination playlist no longer exists")
		}
	}
	for _, s := range inventory.Saved {
		if s.UUID != source.UUID && s.ParentPath == position.parentPath && s.Name == source.Name {
			return position, moveRefusal("smartlist_name_conflict", "Smartlist title already exists under this parent")
		}
	}
	if strings.HasPrefix(before, "smart:") {
		uuid := strings.TrimPrefix(before, "smart:")
		if uuid == source.UUID {
			return position, moveRefusal("smartlist_move_self", "a Smartlist cannot precede itself")
		}
		for _, s := range inventory.Saved {
			if s.UUID == uuid && s.ParentPath == position.parentPath {
				position.nextUUID = uuid
				break
			}
		}
		if position.nextUUID == "" {
			return position, moveRefusal("smartlist_move_anchor", "insertion sibling is not in the destination folder")
		}
	} else if before != "" {
		id, err := strconv.ParseInt(before, 10, 64)
		if err == nil {
			for _, p := range inventory.Playlists {
				if p.ID == id && p.ParentID == position.parentID {
					position.nextPath = p.Path
					break
				}
			}
		}
		if position.nextPath == "" {
			return position, moveRefusal("smartlist_move_anchor", "insertion sibling is not in the destination folder")
		}
	}
	if source.ParentPath == position.parentPath && source.NextPath == position.nextPath && source.NextUUID == position.nextUUID {
		return position, moveRefusal("smartlist_move_unchanged", "Smartlist already occupies the requested position")
	}
	return position, nil
}

// MoveSmartlist splices one saved definition into the native mixed sibling order.
// Rules and edit time remain opaque and untouched; ordinary rows are never written.
func MoveSmartlist(ctx context.Context, database *sql.DB, uuid, to, before string) (int64, error) {
	if strings.TrimSpace(uuid) == "" {
		return 0, moveRefusal("invalid_parameters", "Smartlist UUID is required")
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
	var source *db.SmartlistRecord
	for i := range inventory.Saved {
		if inventory.Saved[i].UUID == uuid {
			source = &inventory.Saved[i]
			break
		}
	}
	if source == nil {
		return 0, moveRefusal("smartlist_not_found", "Smartlist no longer exists")
	}
	if len(inventory.Issues) != 0 {
		return 0, moveRefusal("playlist_order_invalid", "Smartlist links need repair")
	}
	position, err := smartlistMovePosition(inventory, *source, to, before)
	if err != nil {
		return 0, err
	}
	// Native paths end with a separator, so this temporary parent cannot alias one.
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET parentPlaylistPath='-1' WHERE listUuid=?`, uuid); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist AS target SET nextPlaylistPath=moving.nextPlaylistPath,nextListUuid=moving.nextListUuid FROM (SELECT nextPlaylistPath,nextListUuid FROM Smartlist WHERE listUuid=?) AS moving WHERE target.nextListUuid=?`, uuid, uuid); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET nextPlaylistPath='',nextListUuid=? WHERE COALESCE(parentPlaylistPath,'')=? AND COALESCE(nextPlaylistPath,'')=? AND COALESCE(nextListUuid,'')=?`, uuid, position.parentPath, position.nextPath, position.nextUUID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET nextPlaylistPath=?,nextListUuid=? WHERE listUuid=?`, position.nextPath, position.nextUUID, uuid); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE Smartlist SET parentPlaylistPath=? WHERE listUuid=?`, position.parentPath, uuid); err != nil {
		return 0, err
	}
	final, err := db.ReadSmartlistInventoryFrom(ctx, tx)
	if err != nil {
		return 0, err
	}
	if len(final.Issues) != 0 {
		return 0, moveRefusal("playlist_order_invalid", "move produced invalid native links")
	}
	return position.parentID, tx.Commit()
}
