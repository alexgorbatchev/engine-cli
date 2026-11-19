package track

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// publishInPlace replaces original with output without changing its path: the
// bytes go to a temporary file beside the original, which takes the original's
// mode and ownership and then renames over it atomically. The caller has
// preserved the original bytes in history, so undo restores them at the same
// path. before is the original's observation when its bytes were preserved; a
// file that changed since then is refused and left alone.
func publishInPlace(ctx context.Context, output, original string, before os.FileInfo) error {
	installed, err := installUpgrade(ctx, output, original)
	if err != nil {
		return err
	}
	defer os.Remove(installed) // Published by rename; remove only an unpublished copy.
	if err := os.Chmod(installed, before.Mode().Perm()); err != nil {
		return err
	}
	if err := keepOwner(installed, before); err != nil {
		return fmt.Errorf("keeping the file's owner: %w", err)
	}
	after, err := os.Stat(original)
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("audio changed while tagging; original retained")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(installed, original); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(original))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
