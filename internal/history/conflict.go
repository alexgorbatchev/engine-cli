package history

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// UndoConflictCode is the stable code of a non-forced undo refused because a later change touched the same data, so
// clients can explain it and offer a reviewed preview instead of reading the diagnostic text.
const UndoConflictCode = "undo_conflict"

// undoConflict reports a later change that a non-forced undo must not overwrite.
func undoConflict(format string, args ...any) error {
	return &progress.Error{Code: UndoConflictCode, Cause: fmt.Errorf(format, args...)}
}
