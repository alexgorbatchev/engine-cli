package history

import "context"

// RunOptions describes one protected operation and its pre-snapshot preparation.
type RunOptions struct {
	Operation        string
	Scope            Scope
	Paths            []string
	Prepare          func(context.Context) error
	RepairSmartlists bool
}
