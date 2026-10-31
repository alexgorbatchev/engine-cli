package history

import (
	"context"
	"fmt"
	"path/filepath"
)

// OperationID identifies the already durable Before record only when its
// library, operation and scope match the caller's required ownership. Journals
// use it to reconcile independent commits without inferring timestamps.
func OperationID(ctx context.Context, library, operation string, scope Scope) (string, error) {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	if r == nil {
		return "", fmt.Errorf("operation identity requires collection history")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(library)
	if err != nil {
		return "", err
	}
	if r.entry.Library != abs || r.entry.Operation != operation || r.entry.Scope != scope {
		return "", fmt.Errorf("operation identity does not match the required library, operation and scope")
	}
	return r.entry.ID, nil
}
