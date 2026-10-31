package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
)

type undoOwnership struct {
	lease      *intakelease.Lease
	generation string
}

// Select before acquiring replacement ownership, then validate again under the
// exclusive History owner. No reader lease survives into native acquisition.
func ownUndo(ctx context.Context, lib, dir, id string) (*undoOwnership, error) {
	target, err := observeUndoTarget(ctx, dir, id)
	if err != nil {
		return nil, err
	}
	paths := []string{lib}
	if target.RelocationFrom != "" {
		if _, err := os.Lstat(target.RelocationFrom); !os.IsNotExist(err) {
			return nil, fmt.Errorf("undo conflict: previous library location is occupied")
		}
		paths = append(paths, target.RelocationFrom)
	}
	generation, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	lease, err := intakelease.Replace(ctx, paths...)
	if err != nil {
		return nil, err
	}
	return &undoOwnership{lease: lease, generation: string(generation)}, nil
}

func observeUndoTarget(ctx context.Context, dir, id string) (target *Entry, err error) {
	// Reject absent or invalid history before lock() can enroll storage. Reselect
	// under its owner so the copied target reflects one complete publication.
	if err := checkSmartlistRepair(dir); err != nil {
		return nil, err
	}
	all, err := entries(dir)
	if err != nil {
		return nil, err
	}
	if _, err := selectUndoTarget(all, id); err != nil {
		return nil, err
	}
	owner, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	if err := checkSmartlistRepair(dir); err != nil {
		return nil, err
	}
	all, err = entries(dir)
	if err != nil {
		return nil, err
	}
	return selectUndoTarget(all, id)
}

func (o *undoOwnership) validate(target *Entry) error {
	generation, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if string(generation) != o.generation {
		return errors.New("undo target changed while acquiring library replacement ownership; review it again")
	}
	return nil
}

func selectUndoTarget(all []Entry, id string) (*Entry, error) {
	var target *Entry
	undone := make(map[string]bool)
	for i := range all {
		e := &all[i]
		if e.State == "complete" && e.UndoOf != "" && !undone[e.ID] {
			undone[e.UndoOf] = true
		}
		if target == nil && (e.ID == id || (id == "" && e.UndoOf == "" && !undone[e.ID])) {
			target = e
		}
	}
	if target == nil {
		return nil, fmt.Errorf("operation %q not found in library history", id)
	}
	if undone[target.ID] {
		return nil, fmt.Errorf("operation %s has already been undone", target.ID)
	}
	return target, nil
}
