package history

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// RelocateLibrary moves a complete library and rebases its portable journal.
// The recorder follows the directory, so snapshots and undo remain reachable.
func RelocateLibrary(ctx context.Context, from, to string) error {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	if r == nil || r.entry.Library != from {
		return fmt.Errorf("library relocation requires its active undo journal")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(to, 0700); err != nil {
		return err
	}
	lockPath := filepath.Join(from, ".engine-cli", "history", "lock.db")
	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		return errors.Join(err, os.Remove(to))
	}
	if err = os.Remove(to); err != nil {
		return err
	}
	err = os.Rename(from, to)
	copied := false
	if errors.Is(err, syscall.EXDEV) || errors.Is(err, fs.ErrPermission) {
		if err = os.Mkdir(to, 0700); err != nil {
			return err
		}
		copied = true
		err = copyLibraryTree(ctx, from, to, lockPath)
	}
	if err != nil {
		if copied {
			return errors.Join(err, os.RemoveAll(to))
		}
		return err
	} // Exclusively reserved, never a pre-existing destination.
	r.mu.Lock()
	r.entry.RelocationFrom = from
	relocateEntry(r.entry, to)
	r.dir = filepath.Join(to, ".engine-cli", "history")
	err = writeEntry(r.dir, r.entry)
	r.entry.cleanup = func() error {
		if !copied {
			return nil
		}
		current, e := os.Stat(lockPath)
		if e == nil && os.SameFile(lockInfo, current) {
			if e = os.Remove(lockPath); e != nil {
				return e
			}
		}
		for _, path := range []string{filepath.Dir(lockPath), filepath.Dir(filepath.Dir(lockPath)), from} {
			_ = os.Remove(path) /* Only empty remnants of the owned source are removed. */
		}
		return nil
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if copied {
		return removeCopiedTree(ctx, from, to, lockPath, r.entry.ID+".json")
	}
	return nil
}

func copyLibraryTree(ctx context.Context, from, to, lock string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if path == lock || strings.HasPrefix(path, lock+"-") {
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, e := os.Readlink(path)
			if e != nil {
				return e
			}
			return os.Symlink(link, target)
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if strings.HasSuffix(path, ".db"+suffix) {
				return nil
			}
		}
		if filepath.Ext(path) == ".db" {
			return copyDatabase(ctx, path, target)
		}
		return copyTreeFile(ctx, path, target)
	})
}

func copyTreeFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("library contains an unsupported special file")
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, &contextReader{ctx: ctx, r: in})
	return errors.Join(err, out.Sync(), out.Close())
}

func removeCopiedTree(ctx context.Context, from, to, lock, activeEntry string) error {
	var paths []string
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if path == lock || strings.HasPrefix(path, lock+"-") {
			return nil
		}
		if !entry.IsDir() {
			rel, e := filepath.Rel(from, path)
			if e != nil {
				return e
			}
			target := filepath.Join(to, rel)
			switch {
			case path == filepath.Join(filepath.Dir(lock), activeEntry):
			case entry.Type()&os.ModeSymlink != 0:
				a, e := os.Readlink(path)
				if e != nil {
					return e
				}
				b, e := os.Readlink(target)
				if e != nil {
					return e
				}
				if a != b {
					return fmt.Errorf("library link changed while moving")
				}
			case filepath.Ext(path) == ".db":
				a, e := fingerprint(ctx, path)
				if e != nil {
					return e
				}
				b, e := fingerprint(ctx, target)
				if e != nil {
					return e
				}
				if a != b {
					return fmt.Errorf("library database changed while moving")
				}
			case strings.HasSuffix(path, ".db-wal"), strings.HasSuffix(path, ".db-shm"), strings.HasSuffix(path, ".db-journal"):
			default:
				a, e := fileHash(path)
				if e != nil {
					return e
				}
				b, e := fileHash(target)
				if e != nil {
					return e
				}
				if a != b {
					return fmt.Errorf("library file changed while moving")
				}
			}
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	// Every input is verified before consuming any source file.
	for i := len(paths) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return err
		}
		info, e := os.Lstat(paths[i])
		if e != nil {
			return e
		}
		if e = os.Remove(paths[i]); e != nil && !info.IsDir() {
			return e
		}
	}
	return nil
}
