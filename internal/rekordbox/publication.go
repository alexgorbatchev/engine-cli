package rekordbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

type archivePublication struct {
	library, revision string
	archive           importsource.State
}

// commitAndPublish finishes the caller's already validated Engine transaction.
// Independent companion publication cannot roll back that commit; collection
// history must retain its pending before-image if publication fails.
func commitAndPublish(ctx context.Context, tx *sql.Tx, publication archivePublication) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing imported collection: %w", err)
	}
	if err := importsource.Replace(ctx, publication.library, publication.revision, publication.archive); err != nil {
		return errors.Join(fmt.Errorf("publishing imported source evidence: %w", err), history.RequireRecovery(ctx))
	}
	return nil
}
