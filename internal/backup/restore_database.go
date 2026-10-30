package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"zombiezen.com/go/sqlite"
)

func restoreDatabase(ctx context.Context, source, destination string) error {
	// Even a read-only SQLite connection can recover/clean up a rollback
	// journal. Require its owner to recover it before probing this destination.
	if _, err := os.Lstat(destination + "-journal"); err == nil {
		return fmt.Errorf("restore destination has a rollback journal; recover it before restoring")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Keep valid, potentially open databases on SQLite's publication path. A
	// corrupt destination cannot accept a backup transaction. Its repair uses
	// the caller's existing Desktop/write exclusion, not a corruption code as
	// evidence of liveness. Sidecar-bearing corrupt files require recovery.
	_, err := history.DatabaseFingerprint(ctx, destination)
	code := sqlite.ErrCode(err).ToPrimary()
	if code != sqlite.ResultNotADB && code != sqlite.ResultCorrupt {
		return history.SnapshotDatabase(ctx, source, destination)
	}
	dir, err := os.MkdirTemp(filepath.Dir(destination), ".restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	image := filepath.Join(dir, "snapshot.db")
	if _, err := snapshotDatabase(ctx, source, image); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishRepair(image, destination)
}

func publishRepair(image, destination string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(destination + suffix); err == nil {
			return fmt.Errorf("corrupt restore destination has a %s sidecar; recover it before restoring", suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(image, destination)
}
