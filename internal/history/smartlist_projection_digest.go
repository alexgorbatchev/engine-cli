package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func smartlistProjectionDigest(ctx context.Context, dir string, reader db.Reader, inventory *db.SmartlistInventory, columns []string, rows map[string][]any) (string, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	if err := encoder.Encode([]any{[]byte(inventory.EngineUUID), []byte(inventory.PlaylistSchema), inventory.HasSmartlist}); err != nil {
		return "", err
	}
	if inventory.HasSmartlist {
		var schema string
		if err := reader.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='Smartlist'`).Scan(&schema); err != nil {
			return "", err
		}
		if err := encoder.Encode([]byte(schema)); err != nil {
			return "", err
		}
	}
	for _, playlist := range inventory.Playlists {
		if err := encoder.Encode([]any{playlist.ID, playlist.ParentID, []byte(playlist.Path)}); err != nil {
			return "", err
		}
	}
	for _, column := range columns {
		if err := encoder.Encode([]byte(column)); err != nil {
			return "", err
		}
	}
	// Inventory is UUID-sorted. Hash every original row, including omissions,
	// before projection changes link values. Preserve invalid UTF-8 and SQL types.
	for _, row := range inventory.Saved {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		for _, value := range rows[row.UUID] {
			kind := fmt.Sprintf("%T", value)
			if text, ok := value.(string); ok {
				value = []byte(text)
			}
			if err := encoder.Encode([]any{kind, value}); err != nil {
				return "", err
			}
		}
	}
	if err := hashSmartlistEntries(ctx, dir, encoder); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
