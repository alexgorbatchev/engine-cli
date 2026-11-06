// Package watchfs reads watched sources through retained native directories.
package watchfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
)

// Directory pins the canonical directory explicitly authorized by its caller.
// Children are opened one component at a time without following links.
type Directory struct {
	file *os.File
	path string
}

// Open canonicalizes the explicitly selected root once. Later checks never
// resolve a replacement link or repin a different directory.
func Open(path string) (*Directory, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	file, err := openDirectory(canonical)
	if err != nil {
		return nil, err
	}
	return &Directory{file: file, path: canonical}, nil
}

func (d *Directory) Path() string { return d.path }

// Identity observes the retained native directory, never a re-resolved path.
func (d *Directory) Identity() (string, error) { return fileidentity.IdentityFile(d.file) }

func (d *Directory) Close() error { return d.file.Close() }

// Check refuses renamed, replaced or inaccessible authorization paths.
func (d *Directory) Check() (err error) {
	current, err := openDirectory(d.path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	return sameDirectory(d.file, current)
}

// Entries starts a fresh directory enumeration and validates the pinned identity.
func (d *Directory) Entries() (entries []os.DirEntry, err error) {
	current, err := openDirectory(d.path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	if err := sameDirectory(d.file, current); err != nil {
		return nil, err
	}
	return current.ReadDir(-1)
}

func (d *Directory) OpenDirectory(name string) (*Directory, error) {
	file, err := d.openChild(name, true)
	if err != nil {
		return nil, err
	}
	return &Directory{file: file, path: filepath.Join(d.path, name)}, nil
}

// OpenFile accepts only a retained regular file, never a source link or device.
func (d *Directory) OpenFile(name string) (*os.File, error) {
	return d.openChild(name, false)
}

func (d *Directory) openChild(name string, directory bool) (*os.File, error) {
	if !component(name) {
		return nil, &os.PathError{Op: "open watched child", Path: name, Err: fs.ErrInvalid}
	}
	if err := d.Check(); err != nil {
		return nil, err
	}
	return nativeChild(d.file, name, directory)
}

func component(name string) bool {
	return filepath.IsLocal(name) && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsRune(name, 0) && filepath.VolumeName(name) == ""
}

func sameDirectory(want, current *os.File) error {
	a, err := want.Stat()
	if err != nil {
		return err
	}
	b, err := current.Stat()
	if err != nil {
		return err
	}
	if !a.IsDir() || !b.IsDir() || !os.SameFile(a, b) {
		return fmt.Errorf("watched directory identity changed: %w", fs.ErrInvalid)
	}
	return nil
}

func openDirectory(path string) (*os.File, error) {
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	current, err := nativeRoot(root)
	if err != nil {
		return nil, err
	}
	relative := strings.TrimPrefix(path[len(volume):], string(filepath.Separator))
	if relative == "" {
		return current, nil
	}
	for name := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if !component(name) {
			return nil, errors.Join(fs.ErrInvalid, current.Close())
		}
		next, openErr := nativeChild(current, name, true)
		closeErr := current.Close()
		if err := errors.Join(openErr, closeErr); err != nil {
			if next != nil {
				err = errors.Join(err, next.Close())
			}
			return nil, err
		}
		current = next
	}
	return current, nil
}
