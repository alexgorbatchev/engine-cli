package rekordbox

import (
	"context"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const targetCode = "rekordbox_import_target"

// destinationIdentity is checked during review and again in the write
// transaction; an existing root is never treated as an append destination.
func destinationIdentity(ctx context.Context, d db.Executor, name string) (string, error) {
	var uuid string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return "", fmt.Errorf("reading import destination identity: %w", err)
	}
	if uuid == "" {
		return "", importError(targetCode, "destination library has no native identity")
	}
	var count int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM Playlist WHERE parentListId=0 AND title=?", name).Scan(&count); err != nil {
		return "", fmt.Errorf("checking new import root: %w", err)
	}
	if count != 0 {
		return "", importError(targetCode, "choose an unused name for the new import root")
	}
	return uuid, nil
}
