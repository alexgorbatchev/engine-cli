package state

import (
	"context"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

// RecordUndo reconciles registry tombstones after physical library restoration.
func (s *Store) RecordUndo(ctx context.Context, entry *history.Entry) error {
	entries, err := history.List(entry.Library)
	if err != nil {
		return err
	}
	lifecycle := entry.RelocationFrom != ""
	for _, target := range entries {
		if target.ID == entry.UndoOf && (target.Operation == "library.delete" || target.Operation == "library.move" || target.Operation == "library.create") {
			lifecycle = true
		}
	}
	if !lifecycle {
		return nil
	}
	if _, err = os.Stat(filepath.Join(entry.Library, "Database2", "m.db")); err == nil {
		hidden := false
		if err = s.Update(ctx, entry.Library, nil, &hidden); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if err = s.Retire(ctx, entry.Library); err != nil {
			return err
		}
	} else {
		return err
	}
	if entry.RelocationFrom != "" {
		return s.Retire(ctx, entry.RelocationFrom)
	}
	return nil
}
