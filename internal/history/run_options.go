package history

import "context"

// RunOptions describes one operation and its preparation before the Before snapshot.
type RunOptions struct {
	Operation string
	Scope     Scope
	Paths     []string
	Prepare   func(context.Context) error
}
