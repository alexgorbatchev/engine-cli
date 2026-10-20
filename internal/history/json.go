package history

import (
	"context"
	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

// WriteJSON preserves an affected companion file and atomically publishes its
// replacement. Call within Run so the write participates in library undo.
func WriteJSON(ctx context.Context, path string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Preserve(ctx, path); err != nil {
		return err
	}
	return sidecar.WriteJSON(ctx, path, value)
}
