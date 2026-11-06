package history

import (
	"context"
	"database/sql"
)

// RunOptions describes one protected operation and its pre-snapshot preparation.
type RunOptions struct {
	Operation        string
	Scope            Scope
	Paths            []string
	Prepare          func(context.Context) error
	RepairSmartlists bool
	// ObserveSmartlists runs before collection, repair, or backup. Its native
	// transaction reserves the current generation while read-only planning and
	// review validation run. The callback must not change the database. Returning
	// false skips preparation and mutation while still checking cleanup errors.
	ObserveSmartlists func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error)
}
