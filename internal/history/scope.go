package history

import "fmt"

// Scope states what an operation can change, and therefore what its history captures.
type Scope string

const (
	// ScopeCollection operations can change Engine databases, so history snapshots
	// every database before and after them.
	ScopeCollection Scope = "collection"
	// ScopeFiles operations change only files they register, such as staged
	// acquisitions or server files outside the collection. They never write Engine
	// databases, so history copies none.
	ScopeFiles Scope = "files"
)

func (s Scope) capturesDatabases() bool { return s == ScopeCollection }

func (s Scope) validate() error {
	switch s {
	case ScopeCollection, ScopeFiles:
		return nil
	}
	return fmt.Errorf("unknown history scope %q", s)
}
