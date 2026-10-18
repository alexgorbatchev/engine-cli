package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"zombiezen.com/go/sqlite"
)

type fileState struct {
	Hash string      `json:"hash"`
	Mode fs.FileMode `json:"mode"`
}
type databaseState struct {
	Path string `json:"snapshot"`
	Hash string `json:"hash"`
}
type snapshot struct {
	Databases map[string]databaseState `json:"databases"`
	Files     map[string]fileState     `json:"files"`
}

func capture(ctx context.Context, lib, dir, objects string, extra []string) (*snapshot, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &snapshot{Databases: make(map[string]databaseState), Files: make(map[string]fileState)}
	paths := append([]string{lib}, extra...)
	for _, base := range []string{lib, filepath.Join(lib, "Database2")} {
		entries, err := os.ReadDir(base)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".db" {
				continue
			}
			path := filepath.Join(base, entry.Name())
			target := filepath.Join(dir, hashText(path)+".db")
			if err := copyDatabase(ctx, path, target); err != nil {
				return nil, err
			}
			digest, err := fingerprint(ctx, target)
			if err != nil {
				return nil, err
			}
			s.Databases[path] = databaseState{Path: target, Hash: digest}
		}
	}
	if main, ok := s.Databases[filepath.Join(lib, "Database2", "m.db")]; ok {
		c, err := open(ctx, main.Path, sqlite.OpenReadOnly)
		if err != nil {
			return nil, err
		}
		references, err := queryStrings(c, "SELECT path FROM Track WHERE path IS NOT NULL AND path<>''")
		closeErr := c.Close()
		if err = errors.Join(err, closeErr); err != nil {
			return nil, err
		}
		for _, path := range references {
			if !filepath.IsAbs(path) {
				path = filepath.Join(lib, path)
			}
			paths = append(paths, path)
		}
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if path == filepath.Dir(filepath.Dir(objects)) && entry.IsDir() {
				return fs.SkipDir
			}
			if entry.IsDir() {
				return nil
			}
			for dbPath := range s.Databases {
				if path == dbPath || path == dbPath+"-wal" || path == dbPath+"-shm" || path == dbPath+"-journal" {
					return nil
				}
			}
			if seen[path] {
				return nil
			}
			seen[path] = true
			state, err := saveFile(ctx, path, objects)
			if err != nil {
				return err
			}
			s.Files[path] = state
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func hashText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func saveFile(ctx context.Context, path, objects string) (fileState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileState{}, err
	}
	if !info.Mode().IsRegular() {
		return fileState{}, fmt.Errorf("cannot preserve non-regular file %s for undo", path)
	}
	in, err := os.Open(path)
	if err != nil {
		return fileState{}, err
	}
	defer in.Close()
	if err = os.MkdirAll(objects, 0o700); err != nil {
		return fileState{}, err
	}
	out, err := os.CreateTemp(objects, "pending-")
	if err != nil {
		return fileState{}, err
	}
	defer os.Remove(out.Name()) // Only this incomplete content object is temporary.
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), &contextReader{ctx: ctx, r: in})
	err = errors.Join(err, out.Sync(), out.Close())
	if err != nil {
		return fileState{}, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return fileState{}, err
	}
	if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return fileState{}, fmt.Errorf("file changed while preserving it: %s", path)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	target := filepath.Join(objects, digest)
	// Publish the freshly verified content even when an object already exists:
	// a damaged retained object must not cause the only good copy to be lost.
	if err = os.Rename(out.Name(), target); err != nil {
		return fileState{}, err
	}
	return fileState{Hash: digest, Mode: info.Mode().Perm()}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func fileHash(path string) (fileState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return fileState{}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	if !info.Mode().IsRegular() {
		return fileState{}, fmt.Errorf("path is no longer a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fileState{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return fileState{}, err
	}
	return fileState{Hash: hex.EncodeToString(h.Sum(nil)), Mode: info.Mode().Perm()}, nil
}

func restoreFile(ctx context.Context, path, objects string, state fileState) error {
	if state.Hash == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if strings.ContainsAny(state.Hash, "/\\") {
		return fmt.Errorf("invalid saved file hash")
	}
	src := filepath.Join(objects, state.Hash)
	saved, err := fileHash(src)
	if err != nil {
		return err
	}
	if saved.Hash != state.Hash {
		return fmt.Errorf("saved file content is missing or damaged: %s", state.Hash)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(path), ".engine-undo-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, &contextReader{ctx: ctx, r: in})
	err = errors.Join(err, out.Chmod(state.Mode), out.Sync(), out.Close())
	if err != nil {
		return err
	}
	return os.Rename(out.Name(), path)
}

func changedKeys[T comparable](before, after map[string]T) []string {
	keys := make(map[string]bool)
	for k, v := range before {
		if v != after[k] {
			keys[k] = true
		}
	}
	for k, v := range after {
		if v != before[k] {
			keys[k] = true
		}
	}
	result := make([]string, 0, len(keys))
	for k := range keys {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}
