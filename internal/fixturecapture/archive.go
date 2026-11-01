// Package fixturecapture captures explicitly offline Desktop handovers. It never
// opens an input database through SQLite; recovery occurs only on working copies.
package fixturecapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

type File struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified_ns"`
	SHA256   string `json:"sha256"`
}

type Image struct {
	Files       []File            `json:"files"`
	Directories map[string]string `json:"directories"`
}

type Report struct {
	Scope   string `json:"scope"`
	Privacy string `json:"privacy"`
	Before  Image  `json:"before"`
	After   Image  `json:"after"`
}

// Capture requires offline inputs and a new output directory. Observed changes
// fail the capture; matching observations cannot establish live-copy atomicity.
// A failed output is retained for diagnosis and must never be reused.
func Capture(ctx context.Context, before, after, output string) (_ *Report, err error) {
	defer func() {
		if progress.ErrorCode(err) == "operation_failed" {
			err = &progress.Error{Code: "fixture_capture_failed", Cause: err}
		}
	}()
	a, err := watchfs.Open(before)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, a.Close()) }()
	b, err := watchfs.Open(after)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, b.Close()) }()
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absoluteOutput))
	if err != nil {
		return nil, err
	}
	out := filepath.Join(parent, filepath.Base(absoluteOutput))
	for _, source := range []string{a.Path(), b.Path()} {
		if contains(source, out) || contains(out, source) {
			return nil, invalidInput("output overlaps input")
		}
	}
	if contains(a.Path(), b.Path()) || contains(b.Path(), a.Path()) {
		return nil, invalidInput("inputs overlap")
	}
	if err := checkNativeOverlap(a, b, parent); err != nil {
		return nil, err
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, &progress.Error{Code: "fixture_capture_output_exists", Cause: err}
		}
		return nil, err
	}
	r := &Report{Scope: "offline handover; observed byte and identity stability, not a live-copy guarantee", Privacy: "private raw evidence; not approved for publication"}
	for _, entry := range []struct {
		name  string
		dir   *watchfs.Directory
		image *Image
	}{{"before", a, &r.Before}, {"after", b, &r.After}} {
		dest := filepath.Join(out, "archive", entry.name)
		if err := os.MkdirAll(dest, 0o700); err != nil {
			return nil, err
		}
		image, err := inventory(ctx, entry.dir, dest)
		if err != nil {
			return nil, err
		}
		*entry.image = image
	}
	// Archive both inputs before opening any working database.
	if err := verifyImages(ctx, a, b, r); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(out, "archive.json"), r); err != nil {
		return nil, err
	}
	for _, name := range []string{"before", "after"} {
		if err := snapshotImage(ctx, filepath.Join(out, "archive", name), filepath.Join(out, "working", name), filepath.Join(out, "snapshots", name)); err != nil {
			return nil, err
		}
	}
	if err := verifyImages(ctx, a, b, r); err != nil {
		return nil, err
	}
	if err := compareImages(out, r.Before, r.After); err != nil {
		return nil, err
	}
	return r, writeJSON(filepath.Join(out, "complete.json"), r)
}

// Re-observe all input bytes and directory identities, including sidecars.
func verifyImages(ctx context.Context, a, b *watchfs.Directory, r *Report) error {
	for _, entry := range []struct {
		dir   *watchfs.Directory
		image Image
	}{{a, r.Before}, {b, r.After}} {
		current, err := inventory(ctx, entry.dir, "")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(entry.image, current) {
			return inputChanged("offline input changed during capture")
		}
	}
	return nil
}

func contains(parent, child string) bool {
	r, err := filepath.Rel(parent, child)
	return err == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)))
}

func inventory(ctx context.Context, root *watchfs.Directory, destination string) (Image, error) {
	image := Image{Files: []File{}, Directories: map[string]string{}}
	var walk func(*watchfs.Directory, string) error
	walk = func(dir *watchfs.Directory, relative string) (err error) {
		if err := ctx.Err(); err != nil {
			return err
		}
		identity, err := dir.Identity()
		if err != nil {
			return err
		}
		image.Directories[relative] = identity
		entries, err := dir.Entries()
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			name := entry.Name()
			if !utf8.ValidString(name) {
				return invalidInput("unsupported invalid UTF-8 input filename")
			}
			rel := filepath.Join(relative, name)
			if entry.Type()&os.ModeSymlink != 0 {
				return invalidInput("linked input refused: %q", rel)
			}
			if entry.IsDir() {
				child, err := dir.OpenDirectory(name)
				if err != nil {
					return err
				}
				if destination != "" {
					err = os.Mkdir(filepath.Join(destination, rel), 0o700)
				}
				if err == nil {
					err = walk(child, rel)
				}
				if err = errors.Join(err, child.Close()); err != nil {
					return err
				}
				continue
			}
			if strings.Contains(name, ".db-") && !strings.HasSuffix(name, ".db-wal") && !strings.HasSuffix(name, ".db-shm") && !strings.HasSuffix(name, ".db-journal") {
				return invalidInput("unknown database sidecar: %q", rel)
			}
			file, err := dir.OpenFile(name)
			if err != nil {
				return err
			}
			var target io.Writer = io.Discard
			var output *os.File
			if destination != "" {
				output, err = os.OpenFile(filepath.Join(destination, rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err == nil {
					target = output
				}
			}
			var record File
			if err == nil {
				record, err = copyFile(ctx, file, target)
				record.Path = rel
			}
			if output != nil {
				err = errors.Join(err, output.Sync(), output.Close())
			}
			if err = errors.Join(err, file.Close()); err != nil {
				return err
			}
			image.Files = append(image.Files, record)
		}
		return dir.Check()
	}
	return image, walk(root, "")
}

func copyFile(ctx context.Context, file *os.File, output io.Writer) (File, error) {
	before, err := file.Stat()
	if err != nil {
		return File{}, err
	}
	id, err := fileidentity.IdentityFile(file)
	if err != nil {
		return File{}, err
	}
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return File{}, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if _, err := io.MultiWriter(output, hash).Write(buffer[:n]); err != nil {
				return File{}, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return File{}, readErr
		}
	}
	after, err := file.Stat()
	if err != nil {
		return File{}, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return File{}, inputChanged("input changed while reading")
	}
	return File{Identity: id, Size: before.Size(), Modified: before.ModTime().UnixNano(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func writeJSON(path string, value any) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err = json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	return file.Sync()
}
