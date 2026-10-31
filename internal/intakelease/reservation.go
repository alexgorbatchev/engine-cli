package intakelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const reservationDirectory = ".engine-cli-intake"

// A reservation owns stable parent entries, never entries inside a library that
// can be renamed or copied. Anchors are persistent: unlinking one would allow a
// second inode to represent a path while another process still holds the first.
type reservation struct {
	files []*os.File
	roots []*os.Root
}

func reservePaths(ctx context.Context, exclusive bool, paths ...string) (_ *reservation, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if filepath.Dir(absolute) == absolute || filepath.Base(absolute) == reservationDirectory {
			return nil, errors.New("intake path coordination requires a library below a parent directory")
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
		if err != nil {
			return nil, err
		}
		name, err := reservationName(parent, filepath.Base(absolute))
		if err != nil {
			return nil, err
		}
		if name == reservationDirectory {
			return nil, errors.New("intake path coordination cannot reserve its own directory")
		}
		canonical = append(canonical, filepath.Join(parent, name))
	}
	slices.Sort(canonical)
	canonical = slices.Compact(canonical)
	r := &reservation{}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.Close())
		}
	}()
	for _, path := range canonical {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := r.acquire(path, exclusive); err != nil {
			if nativeReservationBusy(err) {
				return nil, &progress.Error{Code: "intake_replacing", Cause: err}
			}
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// Existing aliases must use the actual directory entry's name, not a distinct
// anchor derived from an 8.3 alias or differently normalized spelling.
func reservationName(path, name string) (_ string, err error) {
	parent, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return name, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("intake path coordination requires an ordinary library directory")
	}
	target, err := parent.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	identity, err := rootIdentity(target)
	if err != nil {
		return "", err
	}
	file, err := parent.Open(".")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return "", err
	}
	// Normal spelling should not require opening unrelated sibling libraries,
	// whose access permissions need not match the selected library's.
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == name {
			observed, err := reservationEntryIdentity(parent, name)
			if err != nil {
				return "", err
			}
			if observed != identity {
				return "", errors.New("intake library directory changed during path resolution")
			}
			return name, nil
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		observed, err := reservationEntryIdentity(parent, entry.Name())
		if err != nil {
			return "", err
		}
		if identity == observed {
			return entry.Name(), nil
		}
	}
	return "", errors.New("intake library directory changed during path resolution")
}

func reservationEntryIdentity(parent *os.Root, name string) (_ string, err error) {
	candidate, err := parent.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, candidate.Close()) }()
	return rootIdentity(candidate)
}

func (r *reservation) acquire(path string, exclusive bool) error {
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	r.roots = append(r.roots, parent)
	if err := parent.Mkdir(reservationDirectory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := parent.Lstat(reservationDirectory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("intake path coordination requires an ordinary directory")
	}
	anchors, err := parent.OpenRoot(reservationDirectory)
	if err != nil {
		return err
	}
	r.roots = append(r.roots, anchors)
	if err := matchingReservationNames(parent, anchors); err != nil {
		return err
	}
	name := filepath.Base(path)
	if info, err := anchors.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("intake path coordination requires an ordinary file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := openReservationAnchor(anchors, name)
	if err != nil {
		return err
	}
	r.files = append(r.files, file)
	opened, err := fileidentity.IdentityFile(file)
	if err != nil {
		return err
	}
	info, err = file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return errors.New("intake path coordination requires an empty ordinary file")
	}
	current, err := anchors.Lstat(name)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() {
		return errors.New("intake path coordination changed during acquisition")
	}
	if err := lockReservation(file, exclusive); err != nil {
		return err
	}
	current, err = anchors.Lstat(name)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() {
		return errors.New("intake path coordination changed during acquisition")
	}
	observed, err := fileidentity.Identity(filepath.Join(filepath.Dir(path), reservationDirectory, name), current)
	if err != nil {
		return err
	}
	if opened != observed {
		return errors.New("intake path coordination changed during acquisition")
	}
	return checkReservationRoots(path, parent, anchors)
}

func openReservationAnchor(root *os.Root, name string) (*os.File, error) {
	// Separate creation from opening: concurrent create-or-open can return
	// spurious ENOENT on macOS (golang/go#81246).
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		return root.OpenFile(name, os.O_RDWR, 0600)
	}
	return file, err
}

func rootIdentity(root *os.Root) (_ string, err error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return fileidentity.IdentityFile(file)
}

func checkReservationRoots(path string, parent, anchors *os.Root) (err error) {
	fresh, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, fresh.Close()) }()
	current, err := fresh.OpenRoot(reservationDirectory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	for _, pair := range [][2]*os.Root{{parent, fresh}, {anchors, current}} {
		a, err := rootIdentity(pair[0])
		if err != nil {
			return err
		}
		b, err := rootIdentity(pair[1])
		if err != nil {
			return err
		}
		if a != b {
			return errors.New("intake path coordination directory changed during acquisition")
		}
	}
	return nil
}

func (r *reservation) Close() error {
	var err error
	for _, file := range r.files {
		err = errors.Join(err, file.Close())
	}
	r.files = nil
	for i := len(r.roots) - 1; i >= 0; i-- {
		err = errors.Join(err, r.roots[i].Close())
	}
	r.roots = nil
	return err
}
