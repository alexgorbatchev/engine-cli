package history

import (
	"context"
	"errors"
	"os"
)

// ReadSmartlistSafe observes collection and history state without recovering a
// pending repair. Read must have no side effects: when the first publisher
// enrolls history during an optimistic read, its result is discarded and read
// runs again under the publisher lock. Read leases may nest; mutation and Undo
// operations must not run inside the callback.
func ReadSmartlistSafe[T any](ctx context.Context, lib string, read func(context.Context) (T, error)) (result T, err error) {
	dir, err := storeDir(lib)
	if err != nil {
		return result, err
	}
	locked, err := smartlistReadNeedsLock(ctx, dir)
	if err != nil {
		return result, err
	}
	if !locked {
		observed, readErr := read(ctx)
		// A publisher creates lock storage before changing any target. Checking
		// again after the complete read detects publication that began after
		// the first empty-history observation, including completed rollback.
		locked, err = smartlistReadNeedsLock(ctx, dir)
		if err != nil {
			return result, err
		}
		if !locked {
			return observed, readErr
		}
	}
	release, err := acquireSmartlistRead(ctx, dir)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, release()) }()
	if err := checkSmartlistRepair(dir); err != nil {
		return result, err
	}
	return read(ctx)
}

func smartlistReadNeedsLock(ctx context.Context, dir string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := checkSmartlistRepair(dir); err != nil {
		return false, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	return len(files) != 0, err
}
