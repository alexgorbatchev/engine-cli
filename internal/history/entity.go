package history

import (
	"context"
	"encoding/json"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Membership rows have their own identities, but moving one also belongs in the
// history of its track and both playlists, even when those rows did not change.
func membershipEntities(ctx context.Context, namespace, path string, id int64, entities map[string]Entity) error {
	c, err := open(ctx, path, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	defer c.Close()
	cols, err := queryStrings(c, "SELECT name FROM pragma_table_info('PlaylistEntity') WHERE name IN ('listId','trackId','databaseUuid')")
	if err != nil {
		return err
	}
	if len(cols) != 3 {
		return nil
	}
	return sqlitex.ExecuteTransient(c, "SELECT listId,json_array(databaseUuid,trackId) FROM PlaylistEntity WHERE id=?", &sqlitex.ExecOptions{Args: []any{id}, ResultFunc: func(s *sqlite.Stmt) error {
		key, err := json.Marshal([]string{fmt.Sprintf("%s:%d", sqlite.TypeInteger, s.ColumnInt64(0))})
		if err != nil {
			return err
		}
		for table, identity := range map[string]string{"Playlist": string(key), "Track": s.ColumnText(1)} {
			hash := hashText(namespace + "\x00" + table + "\x00" + identity)
			entities[hash] = Entity{ID: hash, Table: table, Key: identity}
		}
		return nil
	}})
}
